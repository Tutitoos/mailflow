package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
)

type Scheduler struct {
	runs     RunStore
	jobs     JobEnqueuer
	activity ActivityMarker
	now      func() time.Time
}

func (scheduler *Scheduler) SetActivityTracker(activity ActivityMarker) {
	scheduler.activity = activity
}

func NewScheduler(runs RunStore, jobs JobEnqueuer) (*Scheduler, error) {
	if runs == nil || jobs == nil {
		return nil, errors.New("sync scheduler requires runs and queue")
	}
	return &Scheduler{runs: runs, jobs: jobs, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (scheduler *Scheduler) StartInitial(ctx context.Context, user, account string) (Run, error) {
	if scheduler.activity != nil {
		_ = scheduler.activity.Touch(ctx, user, account)
	}
	now := scheduler.now()
	window := now.Add(-RecentWindow)
	run, err := scheduler.runs.CreateRun(ctx, CreateRunInput{UserID: user, AccountID: account, Phase: PhaseRecent, Checkpoint: json.RawMessage(`{}`), WindowStart: &window, ScheduledFor: now})
	if err != nil {
		return Run{}, err
	}
	if err := scheduler.enqueue(ctx, user, run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (scheduler *Scheduler) StartReconciliation(ctx context.Context, user, account string, scheduled time.Time) (Run, error) {
	run, err := scheduler.runs.CreateRun(ctx, CreateRunInput{UserID: user, AccountID: account, Phase: PhaseReconcile, Checkpoint: json.RawMessage(`{}`), ScheduledFor: scheduled})
	if err != nil {
		return Run{}, err
	}
	if !scheduled.After(scheduler.now()) {
		if err := scheduler.enqueue(ctx, user, run); err != nil {
			return Run{}, err
		}
	}
	return run, nil
}

func (scheduler *Scheduler) Request(ctx context.Context, user, account string) (Run, error) {
	if scheduler.activity != nil {
		if err := scheduler.activity.Touch(ctx, user, account); err != nil {
			return Run{}, err
		}
	}
	now := scheduler.now()
	recovered, recoverErr := scheduler.runs.RecoverFailedRun(ctx, user, account, now)
	if recoverErr == nil {
		if err := scheduler.enqueue(ctx, user, recovered); err != nil {
			return Run{}, err
		}
		return recovered, nil
	}
	if !errors.Is(recoverErr, ErrRunNotFound) {
		return Run{}, recoverErr
	}
	run, err := scheduler.StartReconciliation(ctx, user, account, now)
	if !errors.Is(err, ErrRunExists) {
		return run, err
	}
	run, err = scheduler.runs.ExpediteReconciliation(ctx, user, account, now)
	if err != nil {
		return Run{}, err
	}
	if run.State == RunQueued {
		if err := scheduler.enqueue(ctx, user, run); err != nil {
			return Run{}, err
		}
	}
	return run, nil
}

func (scheduler *Scheduler) EnqueueDue(ctx context.Context) (int, error) {
	due, err := scheduler.runs.DueRuns(ctx, scheduler.now(), DefaultSyncRunBatch)
	if err != nil {
		return 0, err
	}
	for index, item := range due {
		if err := scheduler.enqueue(ctx, item.UserID, item.Run); err != nil {
			return index, err
		}
	}
	return len(due), nil
}

func (scheduler *Scheduler) enqueue(ctx context.Context, user string, run Run) error {
	payload, err := json.Marshal(runJobPayload{UserID: user, AccountID: run.AccountID, RunID: run.ID, Version: run.Version})
	if err != nil {
		return syncJobError{code: "sync_encode_failed"}
	}
	_, _, err = scheduler.jobs.Enqueue(ctx, SyncExecuteJobKind, payload, queue.EnqueueOptions{IdempotencyKey: fmt.Sprintf("sync:%s:%d", run.ID, run.Version), MaxAttempts: 8})
	if err != nil {
		return syncJobError{code: "sync_enqueue_failed"}
	}
	return nil
}
