package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

type statusQueue struct {
	stats queue.Stats
	err   error
}

func (reader statusQueue) Ping(context.Context) error { return reader.err }
func (reader statusQueue) Stats(context.Context) (queue.Stats, error) {
	return reader.stats, reader.err
}

type statusHeartbeat struct {
	seen time.Time
	err  error
}

func (reader statusHeartbeat) LastSeen(context.Context, string) (time.Time, error) {
	return reader.seen, reader.err
}

type recordingSynchronizer struct {
	calls int
	err   error
}

func (synchronizer *recordingSynchronizer) Request(_ context.Context, _, accountID string) (mailflowsync.Run, error) {
	synchronizer.calls++
	return mailflowsync.Run{ID: "00000000-0000-7000-8000-000000000051", AccountID: accountID, State: mailflowsync.RunQueued}, synchronizer.err
}

func TestStatusDistinguishesEveryOperationalState(t *testing.T) {
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	build := func(databaseErr error, stats queue.Stats, heartbeat time.Time) *Service {
		service := NewServiceWithMetrics("test", metrics.NewService(metrics.NewRegistry(), nil), Options{
			DatabaseProbe: func(context.Context) error { return databaseErr },
			Queue:         statusQueue{stats: stats}, Heartbeats: statusHeartbeat{seen: heartbeat},
		})
		service.now = func() time.Time { return now }
		return service
	}
	if state := build(nil, queue.Stats{}, now).Status(context.Background()).State; state != Healthy {
		t.Fatalf("healthy state=%s", state)
	}
	if state := build(nil, queue.Stats{Retry: 1}, now).Status(context.Background()).State; state != Degraded {
		t.Fatalf("degraded state=%s", state)
	}
	if state := build(nil, queue.Stats{}, now.Add(-time.Minute)).Status(context.Background()).State; state != Stale {
		t.Fatalf("stale state=%s", state)
	}
	if state := build(errors.New("unavailable"), queue.Stats{}, now).Status(context.Background()).State; state != Blocked {
		t.Fatalf("blocked state=%s", state)
	}
}

func TestWorkerHeartbeatUsesBoundedRedisState(t *testing.T) {
	client, prefix := testkit.Redis(t)
	heartbeats, err := NewHeartbeats(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := heartbeats.Touch(context.Background(), "worker", now); err != nil {
		t.Fatal(err)
	}
	seen, err := heartbeats.LastSeen(context.Background(), "worker")
	if err != nil || seen.Sub(now) > time.Millisecond {
		t.Fatalf("seen=%s err=%v", seen, err)
	}
	if _, err := heartbeats.LastSeen(context.Background(), "missing"); !errors.Is(err, ErrHeartbeatMissing) {
		t.Fatalf("missing heartbeat error=%v", err)
	}
}

func TestQueueRetryIsIdempotentAuditedAndPrivate(t *testing.T) {
	ctx := context.Background()
	databaseURL := testkit.PostgresDatabase(t)
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	const ownerID = "00000000-0000-7000-8000-000000000051"
	const secondOwnerID = "00000000-0000-7000-8000-000000000052"
	const accountID = "00000000-0000-7000-8000-000000000053"
	if _, err := pool.Exec(ctx, `insert into users (id,email,name,locale) values ($1,'owner@example.test','owner','en')`, ownerID); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithMetrics("test", metrics.NewService(metrics.NewRegistry(), pool), Options{Pool: pool, Queue: statusQueue{}})
	synchronizer := &recordingSynchronizer{}
	first, created, err := service.RetrySynchronization(ctx, synchronizer, ownerID, accountID, "stable-browser-key")
	if err != nil || !created || first.Result != "queued" {
		t.Fatalf("first=%+v created=%v err=%v", first, created, err)
	}
	duplicate, created, err := service.RetrySynchronization(ctx, synchronizer, ownerID, accountID, "stable-browser-key")
	if err != nil || created || duplicate.ID != first.ID || synchronizer.calls != 1 {
		t.Fatalf("duplicate=%+v created=%v calls=%d err=%v", duplicate, created, synchronizer.calls, err)
	}
	operations, err := service.ListOperations(ctx, ownerID, 10)
	if err != nil || len(operations) != 1 {
		t.Fatalf("operations=%+v err=%v", operations, err)
	}
	other, err := service.ListOperations(ctx, secondOwnerID, 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("other owner operations=%+v err=%v", other, err)
	}
	encoded, _ := json.Marshal(first)
	if string(encoded) == "" || containsAny(string(encoded), accountID, "stable-browser-key", "payload") {
		t.Fatalf("operation exposed private control data: %s", encoded)
	}
	var targetHash, keyHash string
	if err := pool.QueryRow(ctx, `select target_hash,idempotency_key_hash from admin_operations where id=$1::uuid`, first.ID).Scan(&targetHash, &keyHash); err != nil {
		t.Fatal(err)
	}
	if targetHash == accountID || keyHash == "stable-browser-key" || len(targetHash) != 64 || len(keyHash) != 64 {
		t.Fatalf("unsafe audit hashes target=%q key=%q", targetHash, keyHash)
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
