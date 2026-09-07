package logs_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/logs"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

func TestPipelineBatchesQueriesStreamsDebugAndExpires(t *testing.T) {
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
	store, err := logs.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := logs.NewPipeline(io.Discard, "worker", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Attach(store)
	streamed := make(chan logs.Entry, 4)
	pipeline.SetStream(func(_ context.Context, entry logs.Entry) error { streamed <- entry; return nil })
	runContext, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- pipeline.Run(runContext) }()
	logger := slog.New(pipeline)
	logger.Info("queue handled", "event", "queue.handled", "requestId", "request-safe", "subject", "private")

	now := time.Now().UTC()
	until, err := pipeline.SetDebug(ctx, now, time.Minute)
	if err != nil || until == nil || !pipeline.Enabled(ctx, slog.LevelDebug) {
		t.Fatalf("debug lease=%v err=%v", until, err)
	}
	logger.Debug("temporary detail", "event", "sync.page", "operation", "incremental")

	deadline := time.Now().Add(3 * time.Second)
	for len(streamed) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(streamed) != 2 {
		t.Fatalf("streamed logs=%d", len(streamed))
	}
	entries, err := pipeline.Query(ctx, logs.Query{From: now.Add(-time.Minute), Until: now.Add(time.Minute), Service: "worker", Module: "queue", Level: "info", Event: "queue.handled", RequestID: "request-safe", Limit: 10})
	if err != nil || len(entries) != 1 {
		t.Fatalf("filtered logs=%d err=%v", len(entries), err)
	}
	if entries[0].Attributes["subject"] != "[REDACTED]" {
		t.Fatalf("stored sensitive attributes: %#v", entries[0].Attributes)
	}
	if payload := logs.StreamPayload(entries[0]); string(payload) == "" || string(payload) == "null" {
		t.Fatal("missing safe stream payload")
	}

	if _, err := pipeline.SetDebug(ctx, now, 0); err != nil {
		t.Fatal(err)
	}
	if pipeline.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("debug remained active after revocation")
	}
	if _, err := pipeline.SetDebug(ctx, now, logs.MaxDebugDuration+time.Second); err == nil {
		t.Fatal("oversized debug lease accepted")
	}

	old := now.Add(-logs.DefaultRetention - time.Hour)
	if _, err := store.WriteBatch(ctx, []logs.Entry{{OccurredAt: old, Service: "api", Module: "runtime", Level: "info", Event: "runtime.old"}}); err != nil {
		t.Fatal(err)
	}
	deleted, err := pipeline.Cleanup(ctx, now)
	if err != nil || deleted != 1 {
		t.Fatalf("expired logs=%d err=%v", deleted, err)
	}

	cancel()
	if err := <-done; err == nil {
		t.Fatal("pipeline did not report cancellation")
	}
}
