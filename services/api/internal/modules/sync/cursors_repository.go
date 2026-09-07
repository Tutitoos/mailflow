package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CursorRepository struct{ pool *pgxpool.Pool }

func NewCursorRepository(pool *pgxpool.Pool) *CursorRepository { return &CursorRepository{pool: pool} }

func (repository *CursorRepository) Create(ctx context.Context, input CreateCursorInput) (Cursor, error) {
	userID, accountID, err := cursorIDs(input.UserID, input.AccountID)
	if err != nil || !validCursor(input.Kind, input.Value, input.UIDValidity) {
		return Cursor{}, ErrInvalidCursor
	}
	row, err := dbgen.New(repository.pool).CreateSyncCursor(ctx, dbgen.CreateSyncCursorParams{
		Kind: string(input.Kind), Cursor: input.Value, UidValidity: optionalInt64(input.UIDValidity), AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := repository.Get(ctx, input.UserID, input.AccountID); getErr == nil {
			return Cursor{}, ErrCursorExists
		}
		return Cursor{}, ErrCursorNotFound
	}
	if err != nil {
		return Cursor{}, fmt.Errorf("create sync cursor: %w", err)
	}
	return mapCursor(row), nil
}

func (repository *CursorRepository) Get(ctx context.Context, user, account string) (Cursor, error) {
	userID, accountID, err := cursorIDs(user, account)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	row, err := dbgen.New(repository.pool).GetSyncCursorByOwner(ctx, dbgen.GetSyncCursorByOwnerParams{AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Cursor{}, ErrCursorNotFound
	}
	if err != nil {
		return Cursor{}, fmt.Errorf("get sync cursor: %w", err)
	}
	return mapCursor(row), nil
}

func (repository *CursorRepository) Advance(ctx context.Context, input AdvanceCursorInput, apply ApplyChanges) (Cursor, error) {
	userID, accountID, err := cursorIDs(input.UserID, input.AccountID)
	if err != nil || input.ExpectedVersion < 1 || !validCursorValue(input.Value) || apply == nil {
		return Cursor{}, ErrInvalidCursor
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Cursor{}, fmt.Errorf("begin cursor advancement: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	currentRow, err := queries.LockSyncCursorByOwner(ctx, dbgen.LockSyncCursorByOwnerParams{AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Cursor{}, ErrCursorNotFound
	}
	if err != nil {
		return Cursor{}, fmt.Errorf("lock sync cursor: %w", err)
	}
	current := mapCursor(currentRow)
	if current.State != CursorActive {
		return current, ErrResyncRequired
	}
	if current.Version != input.ExpectedVersion {
		return Cursor{}, ErrCursorConflict
	}
	if current.Kind == CursorIMAPUID && (input.UIDValidity == nil || current.UIDValidity == nil || *input.UIDValidity != *current.UIDValidity) {
		invalidated, invalidateErr := queries.InvalidateSyncCursor(ctx, dbgen.InvalidateSyncCursorParams{Reason: pgtype.Text{String: ReasonUIDValidityChanged, Valid: true}, AccountID: accountID})
		if invalidateErr != nil {
			return Cursor{}, fmt.Errorf("invalidate sync cursor: %w", invalidateErr)
		}
		if err := tx.Commit(ctx); err != nil {
			return Cursor{}, fmt.Errorf("commit cursor invalidation: %w", err)
		}
		return mapCursor(invalidated), ErrResyncRequired
	}
	if current.Kind != CursorIMAPUID && input.UIDValidity != nil {
		return Cursor{}, ErrInvalidCursor
	}
	if err := apply(ctx, tx); err != nil {
		return Cursor{}, ErrApplyChanges
	}
	row, err := queries.AdvanceSyncCursor(ctx, dbgen.AdvanceSyncCursorParams{
		Cursor: input.Value, UidValidity: optionalInt64(input.UIDValidity), AccountID: accountID, ExpectedVersion: input.ExpectedVersion,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Cursor{}, ErrCursorConflict
	}
	if err != nil {
		return Cursor{}, fmt.Errorf("advance sync cursor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Cursor{}, fmt.Errorf("commit cursor advancement: %w", err)
	}
	return mapCursor(row), nil
}

func (repository *CursorRepository) Invalidate(ctx context.Context, user, account, reason string) (Cursor, error) {
	userID, accountID, err := cursorIDs(user, account)
	if err != nil || !validInvalidationReason(reason) {
		return Cursor{}, ErrInvalidCursor
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Cursor{}, fmt.Errorf("begin cursor invalidation: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	if _, err := queries.LockSyncCursorByOwner(ctx, dbgen.LockSyncCursorByOwnerParams{AccountID: accountID, UserID: userID}); errors.Is(err, pgx.ErrNoRows) {
		return Cursor{}, ErrCursorNotFound
	} else if err != nil {
		return Cursor{}, fmt.Errorf("lock sync cursor: %w", err)
	}
	row, err := queries.InvalidateSyncCursor(ctx, dbgen.InvalidateSyncCursorParams{Reason: pgtype.Text{String: reason, Valid: true}, AccountID: accountID})
	if err != nil {
		return Cursor{}, fmt.Errorf("invalidate sync cursor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Cursor{}, fmt.Errorf("commit cursor invalidation: %w", err)
	}
	return mapCursor(row), nil
}

func validCursor(kind CursorKind, value json.RawMessage, uidValidity *int64) bool {
	if !validCursorValue(value) {
		return false
	}
	switch kind {
	case CursorGoogleHistory, CursorMicrosoftDelta:
		return uidValidity == nil
	case CursorIMAPUID:
		return uidValidity != nil && *uidValidity > 0
	default:
		return false
	}
}

func validCursorValue(value json.RawMessage) bool {
	return len(value) > 0 && len(value) <= 65536 && json.Valid(value)
}

func validInvalidationReason(reason string) bool {
	return reason == ReasonRemoteCursorInvalid || reason == ReasonUIDValidityChanged || reason == ReasonManualReset
}

func cursorIDs(user, account string) (pgtype.UUID, pgtype.UUID, error) {
	userID, err := uuid.Parse(user)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	accountID, err := uuid.Parse(account)
	return pgtype.UUID{Bytes: userID, Valid: true}, pgtype.UUID{Bytes: accountID, Valid: err == nil}, err
}

func optionalInt64(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}

func mapCursor(row dbgen.SyncCursor) Cursor {
	return Cursor{
		AccountID: uuid.UUID(row.AccountID.Bytes).String(), Kind: CursorKind(row.Kind), Value: append(json.RawMessage(nil), row.Cursor...),
		State: CursorState(row.State), Checkpoint: row.Checkpoint, UIDValidity: int64Pointer(row.UidValidity), Version: row.Version,
		LastSuccessAt: timePointer(row.LastSuccessAt), InvalidatedAt: timePointer(row.InvalidatedAt),
		InvalidationReason: textPointer(row.InvalidationReason), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}

func int64Pointer(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func textPointer(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
