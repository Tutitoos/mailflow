package metrics_test

import (
	"context"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

func TestMetricsPersistRollUpAndExpireWithoutDoubleCounting(t *testing.T) {
	ctx := context.Background()
	databaseURL := testkit.PostgresDatabase(t)
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	base := time.Date(2026, 9, 7, 12, 34, 0, 0, time.UTC)
	labels := map[string]string{"service": "worker", "module": "sync", "provider": "google", "operation": "page", "result": "success"}

	registry := metrics.NewRegistry()
	service := metrics.NewService(registry, pool)
	if err := registry.Add("mailflow_sync_pages_total", 1, labels); err != nil {
		t.Fatal(err)
	}
	if err := registry.Add("mailflow_sync_pages_total", 2, labels); err != nil {
		t.Fatal(err)
	}
	if err := registry.Observe("mailflow_sync_duration_seconds", 0.01, labels); err != nil {
		t.Fatal(err)
	}
	if err := registry.Observe("mailflow_sync_duration_seconds", 0.3, labels); err != nil {
		t.Fatal(err)
	}
	if err := registry.Set("mailflow_queue_depth", string(metrics.Gauge), 7, map[string]string{"service": "worker", "module": "queue"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Flush(ctx, base); err != nil {
		t.Fatal(err)
	}

	// A fresh process can contribute to the same minute without replacing or
	// replaying the first process's counter delta.
	restarted := metrics.NewService(metrics.NewRegistry(), pool)
	if err := restarted.Registry().Add("mailflow_sync_pages_total", 2, labels); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Flush(ctx, base.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Maintain(ctx, base.Truncate(time.Hour).Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Maintain(ctx, base.Truncate(time.Hour).Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	hourly, err := restarted.Query(ctx, metrics.Query{Resolution: metrics.Hour, From: base.Truncate(time.Hour), Until: base.Add(time.Hour), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) != 3 {
		t.Fatalf("hourly series=%d", len(hourly))
	}
	for _, point := range hourly {
		switch point.Name {
		case "mailflow_sync_pages_total":
			if point.Value != 5 || point.Count != 3 || point.Rate == nil {
				t.Fatalf("counter value=%v count=%d", point.Value, point.Count)
			}
		case "mailflow_sync_duration_seconds":
			if point.P50 == nil || point.P95 == nil || *point.P50 != 0.01 || *point.P95 != 0.5 {
				t.Fatalf("unexpected percentiles: %+v", point)
			}
		}
	}
	if err := restarted.Maintain(ctx, base.Truncate(24*time.Hour).Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Maintain(ctx, base.Add(400*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	daily, err := restarted.Query(ctx, metrics.Query{Resolution: metrics.Day, From: base.Truncate(24 * time.Hour), Until: base.Truncate(24 * time.Hour).Add(24 * time.Hour), Limit: 100})
	if err != nil || len(daily) != 3 {
		t.Fatalf("retained daily series=%d err=%v", len(daily), err)
	}

	oldRegistry := metrics.NewRegistry()
	oldService := metrics.NewService(oldRegistry, pool)
	if err := oldRegistry.Add("mailflow_queue_jobs_total", 1, map[string]string{"service": "worker", "operation": "handle", "result": "success"}); err != nil {
		t.Fatal(err)
	}
	old := base.Add(-metrics.MinuteRetention - time.Hour)
	if err := oldService.Flush(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := oldService.Maintain(ctx, base); err != nil {
		t.Fatal(err)
	}
	expired, err := oldService.Query(ctx, metrics.Query{Resolution: metrics.Minute, From: old.Add(-time.Minute), Until: old.Add(time.Minute), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 {
		t.Fatalf("expired minute points=%d", len(expired))
	}
}
