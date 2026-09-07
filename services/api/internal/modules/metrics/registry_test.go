package metrics

import (
	"errors"
	"testing"
)

func TestRegistryRejectsHighCardinalityDimensions(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Add("sync_total", 1, map[string]string{"provider": "google"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Add("sync_total", 1, map[string]string{"account_id": "private"}); err == nil {
		t.Fatal("expected account identifiers to be rejected as dimensions")
	}
	if err := registry.Add("mailflow_sync_total", 1, map[string]string{"operation": "owner@example.test"}); !errors.Is(err, ErrInvalidMetric) {
		t.Fatalf("expected privacy-sensitive value rejection, got %v", err)
	}
}

func TestRegistryKeepsBoundedSeriesAndAddsCounters(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Add("mailflow_queue_jobs_total", 1, map[string]string{"operation": "handle", "result": "success"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Add("mailflow_queue_jobs_total", 2, map[string]string{"operation": "handle", "result": "success"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Add("mailflow_queue_jobs_total", 1, map[string]string{"operation": "handle", "result": "retry"}); err != nil {
		t.Fatal(err)
	}
	points := registry.Snapshot()
	if len(points) != 2 {
		t.Fatalf("expected two bounded series, got %d", len(points))
	}
	for _, point := range points {
		if point.Labels["result"] == "success" && point.Value != 3 {
			t.Fatalf("expected accumulated success count, got %v", point.Value)
		}
	}
}

func TestRegistryDrainsDeltasKeepsGaugesAndRestoresFailures(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Set("mailflow_queue_depth", "gauge", 3, map[string]string{"service": "worker"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Observe("mailflow_http_duration_seconds", 0.2, map[string]string{"service": "api", "result": "success"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Observe("mailflow_http_duration_seconds", 0.8, map[string]string{"service": "api", "result": "success"}); err != nil {
		t.Fatal(err)
	}

	drained := registry.Drain()
	if len(drained) != 2 || len(registry.Snapshot()) != 1 {
		t.Fatalf("drained=%d remaining=%d", len(drained), len(registry.Snapshot()))
	}
	var histogram Point
	for _, point := range drained {
		if point.Kind == Histogram {
			histogram = point
		}
	}
	if histogram.Count != 2 || histogram.Value != 1 || histogram.Min != 0.2 || histogram.Max != 0.8 {
		t.Fatalf("unexpected histogram: %+v", histogram)
	}

	registry.Restore(drained)
	if len(registry.Snapshot()) != 2 {
		t.Fatal("failed flush did not restore histogram delta")
	}
}

func TestRegistryKeepsKindAndCardinalityDefinitionsAcrossFlushes(t *testing.T) {
	registry := NewRegistry()
	registry.maxSeries = 1
	if err := registry.Add("mailflow_jobs_total", 1, map[string]string{"service": "worker"}); err != nil {
		t.Fatal(err)
	}
	registry.Drain()
	if err := registry.Set("mailflow_jobs_total", string(Gauge), 1, map[string]string{"service": "worker"}); !errors.Is(err, ErrInvalidMetric) {
		t.Fatalf("metric kind changed after drain: %v", err)
	}
	if err := registry.Add("mailflow_other_total", 1, map[string]string{"service": "worker"}); !errors.Is(err, ErrSeriesLimit) {
		t.Fatalf("cardinality limit reset after drain: %v", err)
	}
}
