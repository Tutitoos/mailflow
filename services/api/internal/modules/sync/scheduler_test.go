package sync

import (
	"context"
	"testing"
	"time"
)

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
