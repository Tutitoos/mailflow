package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type fakeSyncQueue struct {
	mu   sync.Mutex
	jobs []queue.Job
	seen map[string]queue.Job
}

func (fake *fakeSyncQueue) Enqueue(_ context.Context, kind string, payload json.RawMessage, options queue.EnqueueOptions) (queue.Job, bool, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.seen == nil {
		fake.seen = make(map[string]queue.Job)
	}
	if existing, ok := fake.seen[options.IdempotencyKey]; ok {
		return existing, false, nil
	}
	job := queue.Job{Version: queue.EnvelopeVersion, ID: uuid.NewString(), Kind: kind, Payload: append(json.RawMessage(nil), payload...), MaxAttempts: options.MaxAttempts}
	fake.seen[options.IdempotencyKey] = job
	fake.jobs = append(fake.jobs, job)
	return job, true, nil
}

func (fake *fakeSyncQueue) pop(t *testing.T) queue.Job {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.jobs) == 0 {
		t.Fatal("expected queued sync job")
	}
	job := fake.jobs[0]
	fake.jobs = fake.jobs[1:]
	return job
}

type fakeLeases struct{ held bool }

func (fake *fakeLeases) Acquire(_ context.Context, account string) (Lease, error) {
	if fake.held {
		return Lease{}, ErrLeaseHeld
	}
	fake.held = true
	return Lease{AccountID: account, token: uuid.NewString()}, nil
}
func (fake *fakeLeases) Release(context.Context, Lease) error { fake.held = false; return nil }

type recordingExecutor struct {
	failOnce  bool
	pageCalls map[RunPhase]int
}

func (executor *recordingExecutor) FetchPage(_ context.Context, run Run) (SyncPage, error) {
	if executor.failOnce {
		executor.failOnce = false
		return SyncPage{}, errors.New("synthetic provider failure")
	}
	if executor.pageCalls == nil {
		executor.pageCalls = make(map[RunPhase]int)
	}
	executor.pageCalls[run.Phase]++
	page := executor.pageCalls[run.Phase]
	hasMore := run.Phase == PhaseRecent && page == 1
	checkpoint := json.RawMessage(fmt.Sprintf(`{"page":%d}`, page))
	return SyncPage{Checkpoint: checkpoint, AppliedCount: 1, HasMore: hasMore, Apply: func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into sync_run_effects (run_id,version,phase) values ($1,$2,$3) on conflict do nothing`, run.ID, run.Version, run.Phase)
		return err
	}}, nil
}

type recordingEvents struct{ payloads []json.RawMessage }

func (publisher *recordingEvents) Publish(_ context.Context, _ string, eventType string, payload json.RawMessage) (events.Envelope, error) {
	if eventType != "sync.progress" {
		return events.Envelope{}, errors.New("unexpected event")
	}
	publisher.payloads = append(publisher.payloads, append(json.RawMessage(nil), payload...))
	return events.Envelope{Version: 1, Type: eventType, Payload: payload}, nil
}

func TestOrchestratorPrioritizesRecentMailAndIgnoresDuplicateDelivery(t *testing.T) {
	_, pool, userID, accountID := cursorFixture(t)
	if _, err := pool.Exec(context.Background(), `create table sync_run_effects (run_id uuid, version bigint, phase text, primary key(run_id,version))`); err != nil {
		t.Fatal(err)
	}
	repository := NewRunRepository(pool)
	jobs, leases := &fakeSyncQueue{}, &fakeLeases{}
	executor := &recordingExecutor{}
	publisher := &recordingEvents{}
	registry := metrics.NewRegistry()
	orchestrator, err := NewOrchestrator(repository, jobs, leases, executor, publisher, registry)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 15, 0, 0, 0, time.UTC)
	orchestrator.now = func() time.Time { return now }
	recent, err := orchestrator.StartInitial(context.Background(), userID, accountID)
	if err != nil || recent.Phase != PhaseRecent || recent.WindowStart == nil || !recent.WindowStart.Equal(now.Add(-RecentWindow)) {
		t.Fatalf("initial run = %+v, %v", recent, err)
	}
	first := jobs.pop(t)
	if err := orchestrator.Handle(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Handle(context.Background(), first); err != nil {
		t.Fatalf("duplicate delivery = %v", err)
	}
	second := jobs.pop(t)
	if err := orchestrator.Handle(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	historicalJob := jobs.pop(t)
	var historicalPayload runJobPayload
	_ = json.Unmarshal(historicalJob.Payload, &historicalPayload)
	historical, err := repository.GetRun(context.Background(), userID, accountID, historicalPayload.RunID)
	if err != nil || historical.Phase != PhaseHistorical || !json.Valid(historical.Checkpoint) || string(historical.Checkpoint) == `{}` {
		t.Fatalf("historical run = %+v, %v", historical, err)
	}
	if err := orchestrator.Handle(context.Background(), historicalJob); err != nil {
		t.Fatal(err)
	}
	incrementalJob := jobs.pop(t)
	var incrementalPayload runJobPayload
	_ = json.Unmarshal(incrementalJob.Payload, &incrementalPayload)
	incremental, err := repository.GetRun(context.Background(), userID, accountID, incrementalPayload.RunID)
	if err != nil || incremental.Phase != PhaseIncremental {
		t.Fatalf("incremental run = %+v, %v", incremental, err)
	}
	var effects int
	if err := pool.QueryRow(context.Background(), `select count(*) from sync_run_effects`).Scan(&effects); err != nil || effects != 3 {
		t.Fatalf("committed effects = %d, %v", effects, err)
	}
	if len(publisher.payloads) != 3 || len(registry.Snapshot()) == 0 {
		t.Fatalf("observability events=%d metrics=%d", len(publisher.payloads), len(registry.Snapshot()))
	}
}

func TestOrchestratorRetriesSafelyCancelsAndRunsReconciliation(t *testing.T) {
	_, pool, userID, accountID := cursorFixture(t)
	if _, err := pool.Exec(context.Background(), `create table sync_run_effects (run_id uuid, version bigint, phase text, primary key(run_id,version))`); err != nil {
		t.Fatal(err)
	}
	repository := NewRunRepository(pool)
	jobs, leases := &fakeSyncQueue{}, &fakeLeases{}
	executor := &recordingExecutor{failOnce: true}
	orchestrator, _ := NewOrchestrator(repository, jobs, leases, executor, nil, metrics.NewRegistry())
	now := time.Date(2026, 9, 7, 16, 0, 0, 0, time.UTC)
	orchestrator.now = func() time.Time { return now }
	run, err := orchestrator.StartReconciliation(context.Background(), userID, accountID, now)
	if err != nil {
		t.Fatal(err)
	}
	job := jobs.pop(t)
	if err := orchestrator.Handle(context.Background(), job); err == nil {
		t.Fatal("expected synthetic provider retry")
	}
	requeued, err := repository.GetRun(context.Background(), userID, accountID, run.ID)
	if err != nil || requeued.State != RunQueued || requeued.Version != run.Version {
		t.Fatalf("requeued run = %+v, %v", requeued, err)
	}
	if err := orchestrator.Handle(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	completed, _ := repository.GetRun(context.Background(), userID, accountID, run.ID)
	if completed.State != RunCompleted || completed.AppliedCount != 1 {
		t.Fatalf("completed reconciliation = %+v", completed)
	}

	due, err := repository.DueRuns(context.Background(), now.Add(ReconciliationPeriod), 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("scheduled reconciliation = %+v, %v", due, err)
	}
	future := due[0].Run
	cancelled, err := repository.CancelRun(context.Background(), userID, accountID, future.ID, now)
	if err != nil || cancelled.State != RunCancelled {
		t.Fatalf("cancelled run = %+v, %v", cancelled, err)
	}
	queued, err := orchestrator.EnqueueDue(context.Background())
	if err != nil || queued != 0 {
		t.Fatalf("cancelled due count = %d, %v", queued, err)
	}
}
