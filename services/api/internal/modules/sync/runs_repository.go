package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RunRepository struct{ pool *pgxpool.Pool }

func NewRunRepository(pool *pgxpool.Pool) *RunRepository { return &RunRepository{pool: pool} }

func (repository *RunRepository) CreateRun(ctx context.Context, input CreateRunInput) (Run, error) {
	userID, accountID, err := cursorIDs(input.UserID, input.AccountID)
	if err != nil || !validRunPhase(input.Phase, input.WindowStart) || !validCursorValue(input.Checkpoint) {
		return Run{}, ErrInvalidRun
	}
	runID, err := ids.New()
	if err != nil {
		return Run{}, fmt.Errorf("create sync run ID: %w", err)
	}
	id, _ := uuid.Parse(runID)
	scheduled := input.ScheduledFor.UTC()
	if scheduled.IsZero() {
		scheduled = time.Now().UTC()
	}
	row, err := dbgen.New(repository.pool).CreateSyncRun(ctx, dbgen.CreateSyncRunParams{
		ID: pgtype.UUID{Bytes: id, Valid: true}, Phase: string(input.Phase), Checkpoint: input.Checkpoint,
		WindowStart: optionalTime(input.WindowStart), ScheduledFor: runTimestamp(scheduled), AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		account, accountErr := dbgen.New(repository.pool).GetAccountByUser(ctx, dbgen.GetAccountByUserParams{ID: accountID, UserID: userID})
		if errors.Is(accountErr, pgx.ErrNoRows) {
			return Run{}, ErrRunNotFound
		}
		if accountErr != nil {
			return Run{}, fmt.Errorf("authorize sync run: %w", accountErr)
		}
		if account.DisabledAt.Valid {
			return Run{}, ErrRunNotFound
		}
		return Run{}, ErrRunExists
	}
	if err != nil {
		return Run{}, fmt.Errorf("create sync run: %w", err)
	}
	return mapRun(row), nil
}

func (repository *RunRepository) GetRun(ctx context.Context, user, account, run string) (Run, error) {
	userID, accountID, runID, err := runIDs(user, account, run)
	if err != nil {
		return Run{}, ErrInvalidRun
	}
	row, err := dbgen.New(repository.pool).GetSyncRunByOwner(ctx, dbgen.GetSyncRunByOwnerParams{ID: runID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get sync run: %w", err)
	}
	return mapRun(row), nil
}

func (repository *RunRepository) StartRun(ctx context.Context, user, account, run string, expectedVersion int64, now time.Time) (Run, error) {
	if _, err := repository.GetRun(ctx, user, account, run); err != nil {
		return Run{}, err
	}
	userID, accountID, runID, _ := runIDs(user, account, run)
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("begin sync run: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	row, err := dbgen.New(tx).StartSyncRun(ctx, dbgen.StartSyncRunParams{StartedAt: runTimestamp(now), ID: runID, AccountID: accountID, ExpectedVersion: expectedVersion})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunStale
	}
	if err != nil {
		return Run{}, fmt.Errorf("start sync run: %w", err)
	}
	if _, err := tx.Exec(ctx, `update accounts set sync_state='syncing', updated_at=$1 where id=$2 and user_id=$3 and disabled_at is null`, now.UTC(), accountID, userID); err != nil {
		return Run{}, fmt.Errorf("mark account syncing: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit sync start: %w", err)
	}
	return mapRun(row), nil
}

func (repository *RunRepository) CommitPage(ctx context.Context, input CommitPageInput) (Run, error) {
	userID, accountID, runID, err := runIDs(input.UserID, input.AccountID, input.RunID)
	if err != nil || input.ExpectedVersion < 1 || input.AppliedCount < 0 || !validCursorValue(input.Checkpoint) || input.Apply == nil {
		return Run{}, ErrInvalidRun
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("begin sync page: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	current, err := queries.LockSyncRunByOwner(ctx, dbgen.LockSyncRunByOwnerParams{ID: runID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("lock sync run: %w", err)
	}
	if current.Version != input.ExpectedVersion || current.State != string(RunRunning) || current.CancelRequested {
		return Run{}, ErrRunStale
	}
	if err := input.Apply(ctx, tx); err != nil {
		return Run{}, ErrApplyChanges
	}
	nextState := RunCompleted
	if input.HasMore {
		nextState = RunQueued
	}
	row, err := queries.CommitSyncRunPage(ctx, dbgen.CommitSyncRunPageParams{
		NextState: string(nextState), Checkpoint: input.Checkpoint, AppliedCount: input.AppliedCount,
		CompletedAt: runTimestamp(input.CompletedAt), ID: runID, AccountID: accountID, ExpectedVersion: input.ExpectedVersion,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunStale
	}
	if err != nil {
		return Run{}, fmt.Errorf("commit sync page: %w", err)
	}
	if nextState == RunCompleted {
		if _, err := tx.Exec(ctx, `update accounts set sync_state='idle', updated_at=$1 where id=$2 and user_id=$3 and disabled_at is null`, input.CompletedAt.UTC(), accountID, userID); err != nil {
			return Run{}, fmt.Errorf("mark account idle: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit sync transaction: %w", err)
	}
	return mapRun(row), nil
}

func (repository *RunRepository) RequeueRun(ctx context.Context, user, account, run string, expectedVersion int64, now time.Time) error {
	if _, err := repository.GetRun(ctx, user, account, run); err != nil {
		return err
	}
	_, accountID, runID, _ := runIDs(user, account, run)
	_, err := dbgen.New(repository.pool).RequeueSyncRun(ctx, dbgen.RequeueSyncRunParams{RequeuedAt: runTimestamp(now), ID: runID, AccountID: accountID, ExpectedVersion: expectedVersion})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRunStale
	}
	if err != nil {
		return fmt.Errorf("requeue sync run: %w", err)
	}
	return nil
}

func (repository *RunRepository) FailRun(ctx context.Context, user, account, run string, expectedVersion int64, failureCode string, now time.Time) (Run, error) {
	userID, accountID, runID, err := runIDs(user, account, run)
	if err != nil || len(failureCode) < 6 || len(failureCode) > 101 {
		return Run{}, ErrInvalidRun
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("begin sync failure: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	row, err := dbgen.New(tx).FailSyncRun(ctx, dbgen.FailSyncRunParams{
		FailureCode: pgtype.Text{String: failureCode, Valid: true}, FailedAt: runTimestamp(now), ID: runID, AccountID: accountID,
		ExpectedVersion: expectedVersion, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunStale
	}
	if err != nil {
		return Run{}, fmt.Errorf("fail sync run: %w", err)
	}
	if _, err := tx.Exec(ctx, `update accounts set sync_state='error', updated_at=$1 where id=$2 and user_id=$3 and disabled_at is null`, now.UTC(), accountID, userID); err != nil {
		return Run{}, fmt.Errorf("mark account failed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit sync failure: %w", err)
	}
	return mapRun(row), nil
}

func (repository *RunRepository) RecoverFailedRun(ctx context.Context, user, account string, now time.Time) (Run, error) {
	userID, accountID, err := cursorIDs(user, account)
	if err != nil {
		return Run{}, ErrInvalidRun
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("begin sync recovery: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	row, err := dbgen.New(tx).RecoverFailedSyncRun(ctx, dbgen.RecoverFailedSyncRunParams{AccountID: accountID, UserID: userID, RecoveredAt: runTimestamp(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("recover sync run: %w", err)
	}
	if _, err := tx.Exec(ctx, `update accounts set sync_state='pending', updated_at=$1 where id=$2 and user_id=$3 and disabled_at is null`, now.UTC(), accountID, userID); err != nil {
		return Run{}, fmt.Errorf("mark account recovery pending: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit sync recovery: %w", err)
	}
	return mapRun(row), nil
}

func (repository *RunRepository) CancelRun(ctx context.Context, user, account, run string, now time.Time) (Run, error) {
	userID, accountID, runID, err := runIDs(user, account, run)
	if err != nil {
		return Run{}, ErrInvalidRun
	}
	row, err := dbgen.New(repository.pool).RequestSyncRunCancellation(ctx, dbgen.RequestSyncRunCancellationParams{CancelledAt: runTimestamp(now), ID: runID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("cancel sync run: %w", err)
	}
	return mapRun(row), nil
}

func (repository *RunRepository) DueRuns(ctx context.Context, now time.Time, limit int) ([]DueRun, error) {
	if limit <= 0 || limit > DefaultSyncRunBatch {
		limit = DefaultSyncRunBatch
	}
	rows, err := dbgen.New(repository.pool).ListDueSyncRuns(ctx, dbgen.ListDueSyncRunsParams{DueAt: runTimestamp(now), BatchSize: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list due sync runs: %w", err)
	}
	result := make([]DueRun, 0, len(rows))
	for _, row := range rows {
		result = append(result, DueRun{UserID: uuid.UUID(row.UserID.Bytes).String(), Run: mapDueRun(row)})
	}
	return result, nil
}

func (repository *RunRepository) ExpediteReconciliation(ctx context.Context, user, account string, now time.Time) (Run, error) {
	userID, accountID, err := cursorIDs(user, account)
	if err != nil {
		return Run{}, ErrInvalidRun
	}
	row, err := dbgen.New(repository.pool).ExpediteSyncReconciliation(ctx, dbgen.ExpediteSyncReconciliationParams{RequestedAt: runTimestamp(now), AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("expedite reconciliation: %w", err)
	}
	return mapRun(row), nil
}

func validRunPhase(phase RunPhase, window *time.Time) bool {
	if phase == PhaseRecent {
		return window != nil && !window.IsZero()
	}
	return window == nil && (phase == PhaseHistorical || phase == PhaseIncremental || phase == PhaseReconcile)
}

func runIDs(user, account, run string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	userID, accountID, err := cursorIDs(user, account)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	runUUID, err := uuid.Parse(run)
	return userID, accountID, pgtype.UUID{Bytes: runUUID, Valid: err == nil}, err
}

func optionalTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return runTimestamp(*value)
}

func runTimestamp(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		value = time.Now().UTC()
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func mapRun(row dbgen.SyncRun) Run {
	return Run{ID: uuid.UUID(row.ID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(), Phase: RunPhase(row.Phase), State: RunState(row.State), Checkpoint: append(json.RawMessage(nil), row.Checkpoint...), Version: row.Version, WindowStart: timePointer(row.WindowStart), AppliedCount: row.AppliedCount, CancelRequested: row.CancelRequested, ScheduledFor: row.ScheduledFor.Time.UTC(), StartedAt: timePointer(row.StartedAt), LastSuccessAt: timePointer(row.LastSuccessAt), CompletedAt: timePointer(row.CompletedAt), FailureCode: row.FailureCode.String, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC()}
}

func mapDueRun(row dbgen.ListDueSyncRunsRow) Run {
	return Run{ID: uuid.UUID(row.ID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(), Phase: RunPhase(row.Phase), State: RunState(row.State), Checkpoint: append(json.RawMessage(nil), row.Checkpoint...), Version: row.Version, WindowStart: timePointer(row.WindowStart), AppliedCount: row.AppliedCount, CancelRequested: row.CancelRequested, ScheduledFor: row.ScheduledFor.Time.UTC(), StartedAt: timePointer(row.StartedAt), LastSuccessAt: timePointer(row.LastSuccessAt), CompletedAt: timePointer(row.CompletedAt), FailureCode: row.FailureCode.String, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC()}
}
