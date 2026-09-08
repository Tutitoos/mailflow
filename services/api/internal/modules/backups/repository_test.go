package backups

import (
	"context"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

func TestRepositoryTracksRuntimeFailuresAndSuccess(t *testing.T) {
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
	repository := NewRepository(pool)
	now := time.Date(2026, 9, 8, 3, 0, 0, 0, time.UTC)
	runtime := Runtime{Enabled: true, RepositoryKind: "local", Schedule: "03:00", Timezone: "UTC", NextRunAt: now.Add(24 * time.Hour), HeartbeatAt: now}
	if err := repository.UpdateRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	failed, err := repository.Begin(ctx, "scheduled", "local", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Begin(ctx, "command", "local", now); err != ErrRunActive {
		t.Fatalf("second active run error=%v", err)
	}
	if _, err := repository.Finish(ctx, failed.ID, "failed", "", "verification_failed", 3, 42, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	status, err := repository.Status(ctx, now.Add(2*time.Minute), 1)
	if err != nil || status.State != "degraded" || len(status.Runs) != 1 || status.Runs[0].ErrorCode != "verification_failed" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	success, err := repository.Begin(ctx, "command", "local", now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	completed, err := repository.Finish(ctx, success.ID, "succeeded", "0123456789abcdef", "", 4, 84, now.Add(4*time.Minute))
	if err != nil || completed.SnapshotID == "" {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	status, err = repository.Status(ctx, now.Add(4*time.Minute), 10)
	if err != nil || status.State != "healthy" || status.LastSuccessAt == nil || len(status.Runs) != 2 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestRepositoryMarksInterruptedRuns(t *testing.T) {
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
	repository := NewRepository(pool)
	now := time.Now().UTC()
	if _, err := repository.Begin(ctx, "scheduled", "local", now); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkInterrupted(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var state, code string
	if err := pool.QueryRow(ctx, `select state,error_code from backup_runs limit 1`).Scan(&state, &code); err != nil || state != "failed" || code != "interrupted" {
		t.Fatalf("state=%s code=%s err=%v", state, code, err)
	}
}
