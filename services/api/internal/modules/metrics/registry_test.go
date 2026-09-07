package metrics

import "testing"

func TestRegistryRejectsHighCardinalityDimensions(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Set("sync_total", "counter", 1, map[string]string{"provider": "google"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Set("sync_total", "counter", 1, map[string]string{"account_id": "private"}); err == nil {
		t.Fatal("expected account identifiers to be rejected as dimensions")
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
