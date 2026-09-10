package sync

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type recordingActivityMarker struct{ touched int }

func (marker *recordingActivityMarker) Active(context.Context, string, string) bool {
	return marker.touched > 0
}
func (marker *recordingActivityMarker) Touch(context.Context, string, string) error {
	marker.touched++
	return nil
}

func TestSchedulerExpeditesExistingDailyReconciliationIdempotently(t *testing.T) {
	_, pool, userID, accountID := cursorFixture(t)
	jobs := &fakeSyncQueue{}
	scheduler, err := NewScheduler(NewRunRepository(pool), jobs)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	scheduler.now = func() time.Time { return now }
	future, err := scheduler.StartReconciliation(context.Background(), userID, accountID, now.Add(ReconciliationPeriod))
	if err != nil || len(jobs.jobs) != 0 {
		t.Fatalf("future reconciliation = %+v jobs=%d error=%v", future, len(jobs.jobs), err)
	}
	requested, err := scheduler.Request(context.Background(), userID, accountID)
	if err != nil || requested.ID != future.ID || !requested.ScheduledFor.Equal(now) || len(jobs.jobs) != 1 {
		t.Fatalf("requested reconciliation = %+v jobs=%d error=%v", requested, len(jobs.jobs), err)
	}
	repeated, err := scheduler.Request(context.Background(), userID, accountID)
	if err != nil || repeated.ID != future.ID || len(jobs.jobs) != 1 {
		t.Fatalf("repeated reconciliation = %+v jobs=%d error=%v", repeated, len(jobs.jobs), err)
	}
}

func TestSchedulerPullsIdleIncrementalRunIntoActiveWindowIdempotently(t *testing.T) {
	_, pool, userID, accountID := cursorFixture(t)
	repository := NewRunRepository(pool)
	scheduler, err := NewScheduler(repository, &fakeSyncQueue{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 15, 30, 0, 0, time.UTC)
	scheduler.now = func() time.Time { return now }
	marker := &recordingActivityMarker{}
	scheduler.SetActivityTracker(marker)
	run, err := repository.CreateRun(context.Background(), CreateRunInput{
		UserID: userID, AccountID: accountID, Phase: PhaseIncremental,
		Checkpoint: json.RawMessage(`{"historyId":"10"}`), ScheduledFor: now.Add(IdlePollInterval),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Activate(context.Background(), userID, accountID); err != nil {
		t.Fatal(err)
	}
	active, err := repository.GetRun(context.Background(), userID, accountID, run.ID)
	if err != nil || !active.ScheduledFor.Equal(now.Add(ActivePollInterval)) || marker.touched != 1 {
		t.Fatalf("active run=%+v touches=%d error=%v", active, marker.touched, err)
	}
	if err := scheduler.Activate(context.Background(), userID, accountID); err != nil {
		t.Fatal(err)
	}
	repeated, err := repository.GetRun(context.Background(), userID, accountID, run.ID)
	if err != nil || !repeated.ScheduledFor.Equal(active.ScheduledFor) || marker.touched != 2 {
		t.Fatalf("repeated run=%+v touches=%d error=%v", repeated, marker.touched, err)
	}
}
