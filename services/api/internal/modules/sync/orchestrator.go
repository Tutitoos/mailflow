package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type SyncPage struct {
	Checkpoint   json.RawMessage
	AppliedCount int64
	HasMore      bool
	Apply        func(context.Context, pgx.Tx) error
}

type PageExecutor interface {
	FetchPage(context.Context, string, Run) (SyncPage, error)
}

type ActivityTracker interface {
	Active(context.Context, string, string) bool
}

type JobEnqueuer interface {
	Enqueue(context.Context, string, json.RawMessage, queue.EnqueueOptions) (queue.Job, bool, error)
}

type AccountLeases interface {
	Acquire(context.Context, string) (Lease, error)
	Release(context.Context, Lease) error
}

type EventPublisher interface {
	Publish(context.Context, string, string, json.RawMessage) (events.Envelope, error)
}

type MetricSink interface {
	Add(string, float64, map[string]string) error
}

type Orchestrator struct {
	runs     RunStore
	jobs     JobEnqueuer
	leases   AccountLeases
	executor PageExecutor
	events   EventPublisher
	metrics  MetricSink
	activity ActivityTracker
	now      func() time.Time
}

func (orchestrator *Orchestrator) SetActivityTracker(activity ActivityTracker) {
	orchestrator.activity = activity
}

type runJobPayload struct {
	UserID    string `json:"userId"`
	AccountID string `json:"accountId"`
	RunID     string `json:"runId"`
	Version   int64  `json:"version"`
}

type syncJobError struct{ code string }

func (err syncJobError) Error() string        { return err.code }
func (err syncJobError) JobErrorCode() string { return err.code }

func NewOrchestrator(runs RunStore, jobs JobEnqueuer, leases AccountLeases, executor PageExecutor, publisher EventPublisher, metrics MetricSink) (*Orchestrator, error) {
	if runs == nil || jobs == nil || leases == nil || executor == nil {
		return nil, errors.New("sync orchestrator requires runs, queue, leases, and executor")
	}
	return &Orchestrator{runs: runs, jobs: jobs, leases: leases, executor: executor, events: publisher, metrics: metrics, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (orchestrator *Orchestrator) StartInitial(ctx context.Context, user, account string) (Run, error) {
	now := orchestrator.now()
	window := now.Add(-RecentWindow)
	run, err := orchestrator.runs.CreateRun(ctx, CreateRunInput{UserID: user, AccountID: account, Phase: PhaseRecent, Checkpoint: json.RawMessage(`{}`), WindowStart: &window, ScheduledFor: now})
	if err != nil {
		return Run{}, err
	}
	if err := orchestrator.enqueue(ctx, user, run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (orchestrator *Orchestrator) StartReconciliation(ctx context.Context, user, account string, scheduled time.Time) (Run, error) {
	run, err := orchestrator.runs.CreateRun(ctx, CreateRunInput{UserID: user, AccountID: account, Phase: PhaseReconcile, Checkpoint: json.RawMessage(`{}`), ScheduledFor: scheduled})
	if err != nil {
		return Run{}, err
	}
	if !scheduled.After(orchestrator.now()) {
		if err := orchestrator.enqueue(ctx, user, run); err != nil {
			return Run{}, err
		}
	}
	return run, nil
}

func (orchestrator *Orchestrator) EnqueueDue(ctx context.Context) (int, error) {
	due, err := orchestrator.runs.DueRuns(ctx, orchestrator.now(), DefaultSyncRunBatch)
	if err != nil {
		return 0, err
	}
	for index, item := range due {
		if err := orchestrator.enqueue(ctx, item.UserID, item.Run); err != nil {
			return index, err
		}
	}
	return len(due), nil
}

func (orchestrator *Orchestrator) Handler() queue.Handler {
	return orchestrator.Handle
}

func (orchestrator *Orchestrator) Handle(ctx context.Context, job queue.Job) error {
	var payload runJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || !validRunPayload(payload) {
		return syncJobError{code: "sync_invalid_job"}
	}
	run, err := orchestrator.runs.GetRun(ctx, payload.UserID, payload.AccountID, payload.RunID)
	if errors.Is(err, ErrRunNotFound) || errors.Is(err, ErrInvalidRun) {
		return syncJobError{code: "sync_run_not_found"}
	}
	if err != nil {
		return syncJobError{code: "sync_store_unavailable"}
	}
	if run.Version != payload.Version || run.State == RunCompleted || run.State == RunCancelled {
		return nil
	}
	lease, err := orchestrator.leases.Acquire(ctx, payload.AccountID)
	if errors.Is(err, ErrLeaseHeld) {
		return syncJobError{code: "sync_lease_held"}
	}
	if err != nil {
		return syncJobError{code: "sync_lease_unavailable"}
	}
	defer orchestrator.release(lease)

	run, err = orchestrator.runs.StartRun(ctx, payload.UserID, payload.AccountID, payload.RunID, payload.Version, orchestrator.now())
	if errors.Is(err, ErrRunStale) {
		return nil
	}
	if err != nil {
		return syncJobError{code: "sync_start_failed"}
	}
	page, err := orchestrator.executor.FetchPage(ctx, payload.UserID, run)
	if err != nil {
		if errors.Is(err, ErrRemoteCursorInvalid) {
			_, _ = orchestrator.runs.CancelRun(ctx, payload.UserID, payload.AccountID, payload.RunID, orchestrator.now())
			_, startErr := orchestrator.StartInitial(ctx, payload.UserID, payload.AccountID)
			orchestrator.observe(run.Phase, "resync")
			orchestrator.publish(payload.UserID, run, "resync_required")
			if startErr != nil && !errors.Is(startErr, ErrRunExists) {
				return syncJobError{code: "sync_recovery_failed"}
			}
			return nil
		}
		orchestrator.requeue(payload)
		orchestrator.observe(run.Phase, "retry")
		orchestrator.publish(payload.UserID, run, "queued")
		return syncJobError{code: "sync_provider_failed"}
	}
	if !validPage(page) {
		orchestrator.requeue(payload)
		return syncJobError{code: "sync_invalid_page"}
	}
	committed, err := orchestrator.runs.CommitPage(ctx, CommitPageInput{
		UserID: payload.UserID, AccountID: payload.AccountID, RunID: payload.RunID, ExpectedVersion: payload.Version,
		Checkpoint: page.Checkpoint, AppliedCount: page.AppliedCount, HasMore: page.HasMore, CompletedAt: orchestrator.now(), Apply: page.Apply,
	})
	if errors.Is(err, ErrRunStale) {
		return nil
	}
	if err != nil {
		orchestrator.requeue(payload)
		return syncJobError{code: "sync_commit_failed"}
	}
	orchestrator.observe(run.Phase, "success")
	orchestrator.publish(payload.UserID, committed, string(committed.State))
	if committed.State == RunQueued {
		return orchestrator.enqueue(ctx, payload.UserID, committed)
	}
	return orchestrator.scheduleSuccessor(ctx, payload.UserID, committed)
}

func (orchestrator *Orchestrator) scheduleSuccessor(ctx context.Context, user string, completed Run) error {
	phase := RunPhase("")
	scheduled := orchestrator.now()
	checkpoint := append(json.RawMessage(nil), completed.Checkpoint...)
	switch completed.Phase {
	case PhaseRecent:
		phase = PhaseHistorical
	case PhaseHistorical:
		phase = PhaseIncremental
	case PhaseIncremental:
		interval := ActivePollInterval
		if orchestrator.activity != nil && !orchestrator.activity.Active(ctx, user, completed.AccountID) {
			interval = IdlePollInterval
		}
		phase, scheduled = PhaseIncremental, scheduled.Add(interval)
	case PhaseReconcile:
		phase, scheduled, checkpoint = PhaseReconcile, scheduled.Add(ReconciliationPeriod), json.RawMessage(`{}`)
	}
	if phase == "" {
		return nil
	}
	next, err := orchestrator.runs.CreateRun(ctx, CreateRunInput{UserID: user, AccountID: completed.AccountID, Phase: phase, Checkpoint: checkpoint, ScheduledFor: scheduled})
	if errors.Is(err, ErrRunExists) {
		return nil
	}
	if err != nil {
		return syncJobError{code: "sync_schedule_failed"}
	}
	if completed.Phase == PhaseHistorical {
		_, reconcileErr := orchestrator.runs.CreateRun(ctx, CreateRunInput{
			UserID: user, AccountID: completed.AccountID, Phase: PhaseReconcile,
			Checkpoint: json.RawMessage(`{}`), ScheduledFor: orchestrator.now().Add(ReconciliationPeriod),
		})
		if reconcileErr != nil && !errors.Is(reconcileErr, ErrRunExists) {
			return syncJobError{code: "sync_schedule_failed"}
		}
	}
	if !scheduled.After(orchestrator.now()) {
		return orchestrator.enqueue(ctx, user, next)
	}
	return nil
}

func (orchestrator *Orchestrator) enqueue(ctx context.Context, user string, run Run) error {
	payload, err := json.Marshal(runJobPayload{UserID: user, AccountID: run.AccountID, RunID: run.ID, Version: run.Version})
	if err != nil {
		return syncJobError{code: "sync_encode_failed"}
	}
	_, _, err = orchestrator.jobs.Enqueue(ctx, SyncExecuteJobKind, payload, queue.EnqueueOptions{IdempotencyKey: fmt.Sprintf("sync:%s:%d", run.ID, run.Version), MaxAttempts: 8})
	if err != nil {
		return syncJobError{code: "sync_enqueue_failed"}
	}
	return nil
}

func (orchestrator *Orchestrator) release(lease Lease) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = orchestrator.leases.Release(ctx, lease)
}

func (orchestrator *Orchestrator) requeue(payload runJobPayload) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = orchestrator.runs.RequeueRun(ctx, payload.UserID, payload.AccountID, payload.RunID, payload.Version, orchestrator.now())
}

func (orchestrator *Orchestrator) publish(user string, run Run, state string) {
	if orchestrator.events == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{"accountId": run.AccountID, "runId": run.ID, "phase": run.Phase, "state": state, "appliedCount": run.AppliedCount})
	_, _ = orchestrator.events.Publish(context.Background(), user, "sync.progress", payload)
}

func (orchestrator *Orchestrator) observe(phase RunPhase, result string) {
	if orchestrator.metrics != nil {
		_ = orchestrator.metrics.Add("mailflow_sync_pages_total", 1, map[string]string{"module": "sync", "operation": string(phase), "result": result, "service": "worker"})
	}
}

func validRunPayload(payload runJobPayload) bool {
	_, userErr := uuid.Parse(payload.UserID)
	_, accountErr := uuid.Parse(payload.AccountID)
	_, runErr := uuid.Parse(payload.RunID)
	return userErr == nil && accountErr == nil && runErr == nil && payload.Version > 0
}

func validPage(page SyncPage) bool {
	return page.Apply != nil && page.AppliedCount >= 0 && validCursorValue(page.Checkpoint)
}
