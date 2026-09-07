package sync

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestRunRepositoryCommitsEffectsAndCheckpointAtomically(t *testing.T) {
	_, pool, userID, accountID := cursorFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `create table sync_repository_effects (id integer primary key)`); err != nil {
		t.Fatal(err)
	}
	repository := NewRunRepository(pool)
	now := time.Now().UTC()
	window := now.Add(-RecentWindow)
	run, err := repository.CreateRun(ctx, CreateRunInput{UserID: userID, AccountID: accountID, Phase: PhaseRecent, Checkpoint: json.RawMessage(`{}`), WindowStart: &window, ScheduledFor: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.StartRun(ctx, userID, accountID, run.ID, run.Version, now); err != nil {
		t.Fatal(err)
	}
	committed, err := repository.CommitPage(ctx, CommitPageInput{
		UserID: userID, AccountID: accountID, RunID: run.ID, ExpectedVersion: run.Version,
		Checkpoint: json.RawMessage(`{"page":1}`), AppliedCount: 1, HasMore: true, CompletedAt: now,
		Apply: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `insert into sync_repository_effects values (1)`)
			return err
		},
	})
	if err != nil {
		t.Fatalf("commit page: %v", err)
	}
	if committed.State != RunQueued || committed.Version != 2 || committed.AppliedCount != 1 {
		t.Fatalf("committed run = %+v", committed)
	}
}

func TestRunRepositoryDistinguishesActiveRunFromMissingOwner(t *testing.T) {
	_, pool, userID, accountID := cursorFixture(t)
	repository := NewRunRepository(pool)
	now := time.Now().UTC()
	input := CreateRunInput{UserID: userID, AccountID: accountID, Phase: PhaseReconcile, Checkpoint: json.RawMessage(`{}`), ScheduledFor: now}
	if _, err := repository.CreateRun(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateRun(context.Background(), input); !errors.Is(err, ErrRunExists) {
		t.Fatalf("duplicate run error = %v", err)
	}
	input.AccountID = "0199ed3b-c950-7000-8000-000000000099"
	if _, err := repository.CreateRun(context.Background(), input); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("missing account error = %v", err)
	}
	input.AccountID, input.UserID = accountID, "0199ed3b-c950-7000-8000-000000000099"
	if _, err := repository.CreateRun(context.Background(), input); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("cross-owner account error = %v", err)
	}
}
