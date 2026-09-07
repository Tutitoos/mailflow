package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PendingActionRepository struct{ pool *pgxpool.Pool }

func NewPendingActionRepository(pool *pgxpool.Pool) *PendingActionRepository {
	return &PendingActionRepository{pool: pool}
}

func (repository *PendingActionRepository) Enqueue(ctx context.Context, input EnqueueActionInput, apply ApplyActionState) (PendingAction, bool, error) {
	userID, accountID, targetID, err := actionIDs(input.UserID, input.AccountID, input.TargetID)
	if err != nil || !validActionInput(input) || apply == nil {
		return PendingAction{}, false, ErrInvalidAction
	}
	actionID, err := newActionID()
	if err != nil {
		return PendingAction{}, false, err
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return PendingAction{}, false, fmt.Errorf("begin mail action: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	row, err := queries.CreatePendingAction(ctx, dbgen.CreatePendingActionParams{
		ID: actionID, IdempotencyKey: input.IdempotencyKey, Kind: string(input.Kind),
		TargetKind: string(input.TargetKind), TargetID: targetID, DesiredState: input.DesiredState,
		MaxAttempts: int32(input.MaxAttempts), AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, getErr := queries.GetPendingActionByIdempotency(ctx, dbgen.GetPendingActionByIdempotencyParams{
			AccountID: accountID, IdempotencyKey: input.IdempotencyKey, UserID: userID,
		})
		if errors.Is(getErr, pgx.ErrNoRows) {
			return PendingAction{}, false, ErrActionNotFound
		}
		if getErr != nil {
			return PendingAction{}, false, fmt.Errorf("get idempotent mail action: %w", getErr)
		}
		action := mapPendingAction(existing)
		if action.Kind != input.Kind || action.TargetKind != input.TargetKind || action.TargetID != input.TargetID || !jsonEqual(action.DesiredState, input.DesiredState) {
			return PendingAction{}, false, ErrIdempotencyConflict
		}
		return action, false, nil
	}
	if err != nil {
		return PendingAction{}, false, fmt.Errorf("create mail action: %w", err)
	}
	if err := apply(ctx, tx); err != nil {
		return PendingAction{}, false, ErrOptimisticUpdate
	}
	if err := tx.Commit(ctx); err != nil {
		return PendingAction{}, false, fmt.Errorf("commit mail action: %w", err)
	}
	return mapPendingAction(row), true, nil
}

func (repository *PendingActionRepository) Claim(ctx context.Context, user string) (ActionClaim, error) {
	userID, err := uuidParameter(user)
	if err != nil {
		return ActionClaim{}, ErrInvalidAction
	}
	token, err := newActionID()
	if err != nil {
		return ActionClaim{}, err
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return ActionClaim{}, fmt.Errorf("begin mail action claim: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	row, err := queries.ClaimPendingAction(ctx, dbgen.ClaimPendingActionParams{UserID: userID, ClaimToken: token})
	if errors.Is(err, pgx.ErrNoRows) {
		return ActionClaim{}, ErrActionUnavailable
	}
	if err != nil {
		return ActionClaim{}, fmt.Errorf("claim mail action: %w", err)
	}
	if err := queries.CreatePendingActionAttempt(ctx, dbgen.CreatePendingActionAttemptParams{ActionID: row.ID, Attempt: row.Attempts, ClaimToken: token}); err != nil {
		return ActionClaim{}, fmt.Errorf("record mail action attempt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ActionClaim{}, fmt.Errorf("commit mail action claim: %w", err)
	}
	return ActionClaim{PendingAction: mapPendingAction(row), token: uuid.UUID(token.Bytes).String()}, nil
}

func (repository *PendingActionRepository) Complete(ctx context.Context, user string, claim ActionClaim) (PendingAction, error) {
	return repository.finish(ctx, user, claim, "", time.Time{}, nil, nil)
}

func (repository *PendingActionRepository) Fail(ctx context.Context, user string, claim ActionClaim, code string, availableAt time.Time, authoritative json.RawMessage, restore ApplyActionState) (PendingAction, bool, error) {
	action, err := repository.finish(ctx, user, claim, code, availableAt, authoritative, restore)
	return action, err == nil && action.Status == ActionConflict, err
}

func (repository *PendingActionRepository) finish(ctx context.Context, user string, claim ActionClaim, code string, availableAt time.Time, authoritative json.RawMessage, restore ApplyActionState) (PendingAction, error) {
	userID, actionID, token, err := claimedActionIDs(user, claim)
	if err != nil || (code != "" && !validActionErrorCode(code)) {
		return PendingAction{}, ErrInvalidAction
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return PendingAction{}, fmt.Errorf("begin mail action result: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	row, err := queries.GetClaimedPendingAction(ctx, dbgen.GetClaimedPendingActionParams{ID: actionID, ClaimToken: token, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingAction{}, ErrActionClaimLost
	}
	if err != nil {
		return PendingAction{}, fmt.Errorf("lock claimed mail action: %w", err)
	}
	params := dbgen.FinishPendingActionAttemptParams{ActionID: actionID, Attempt: row.Attempts, ClaimToken: token}
	var result dbgen.PendingAction
	if code == "" {
		result, err = queries.CompletePendingAction(ctx, dbgen.CompletePendingActionParams{ID: actionID, ClaimToken: token})
		params.Status = "completed"
	} else if row.Attempts < row.MaxAttempts {
		if availableAt.IsZero() || !availableAt.After(time.Now().UTC()) {
			return PendingAction{}, ErrInvalidAction
		}
		result, err = queries.RetryPendingAction(ctx, dbgen.RetryPendingActionParams{
			AvailableAt: requiredTime(availableAt), ErrorCode: optionalText(code), ID: actionID, ClaimToken: token,
		})
		params.Status, params.ErrorCode = "retry", optionalText(code)
	} else {
		if !validActionState(authoritative) || restore == nil {
			return PendingAction{}, ErrInvalidAction
		}
		if restoreErr := restore(ctx, tx); restoreErr != nil {
			return PendingAction{}, ErrAuthoritativeUpdate
		}
		result, err = queries.ConflictPendingAction(ctx, dbgen.ConflictPendingActionParams{
			AuthoritativeState: authoritative, ErrorCode: optionalText(code), ID: actionID, ClaimToken: token,
		})
		params.Status, params.ErrorCode = "conflict", optionalText(code)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingAction{}, ErrActionClaimLost
	}
	if err != nil {
		return PendingAction{}, fmt.Errorf("finish mail action: %w", err)
	}
	if err := queries.FinishPendingActionAttempt(ctx, params); err != nil {
		return PendingAction{}, fmt.Errorf("finish mail action attempt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PendingAction{}, fmt.Errorf("commit mail action result: %w", err)
	}
	return mapPendingAction(result), nil
}

func validActionInput(input EnqueueActionInput) bool {
	return validIdempotencyKey(input.IdempotencyKey) && validActionKind(input.Kind) &&
		(input.TargetKind == ActionTargetThread || input.TargetKind == ActionTargetMessage) && validDesiredActionState(input.Kind, input.DesiredState) &&
		input.MaxAttempts >= 1 && input.MaxAttempts <= 20
}

func validIdempotencyKey(key string) bool {
	if len(key) < 16 || len(key) > 128 || strings.TrimSpace(key) != key {
		return false
	}
	for _, character := range key {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && !strings.ContainsRune("._:-", character) {
			return false
		}
	}
	return true
}

func validActionKind(kind ActionKind) bool {
	switch kind {
	case ActionMarkRead, ActionMarkUnread, ActionStar, ActionUnstar, ActionMarkImportant, ActionMarkUnimportant, ActionMoveToTrash, ActionRestoreTrash:
		return true
	default:
		return false
	}
}

func validActionState(value json.RawMessage) bool {
	if len(value) < 2 || len(value) > 8<<10 || !json.Valid(value) {
		return false
	}
	var object map[string]any
	if json.Unmarshal(value, &object) != nil || len(object) < 1 || len(object) > 4 {
		return false
	}
	for key, value := range object {
		if key != "read" && key != "starred" && key != "important" && key != "trashed" {
			return false
		}
		if _, ok := value.(bool); !ok {
			return false
		}
	}
	return true
}

func validDesiredActionState(kind ActionKind, value json.RawMessage) bool {
	if !validActionState(value) {
		return false
	}
	var object map[string]bool
	if json.Unmarshal(value, &object) != nil || len(object) != 1 {
		return false
	}
	wantKey, wantValue := "", false
	switch kind {
	case ActionMarkRead:
		wantKey, wantValue = "read", true
	case ActionMarkUnread:
		wantKey = "read"
	case ActionStar:
		wantKey, wantValue = "starred", true
	case ActionUnstar:
		wantKey = "starred"
	case ActionMarkImportant:
		wantKey, wantValue = "important", true
	case ActionMarkUnimportant:
		wantKey = "important"
	case ActionMoveToTrash:
		wantKey, wantValue = "trashed", true
	case ActionRestoreTrash:
		wantKey = "trashed"
	default:
		return false
	}
	actual, ok := object[wantKey]
	return ok && actual == wantValue
}

func validActionErrorCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for _, character := range code {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return false
		}
	}
	return true
}

func actionIDs(user, account, target string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	userID, err := uuidParameter(user)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	accountID, err := uuidParameter(account)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	targetID, err := uuidParameter(target)
	return userID, accountID, targetID, err
}

func claimedActionIDs(user string, claim ActionClaim) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	userID, err := uuidParameter(user)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	actionID, err := uuidParameter(claim.ID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	token, err := uuidParameter(claim.token)
	return userID, actionID, token, err
}

func uuidParameter(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	return pgtype.UUID{Bytes: parsed, Valid: err == nil}, err
}

func newActionID() (pgtype.UUID, error) {
	value, err := ids.New()
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create mail action ID: %w", err)
	}
	return uuidParameter(value)
}

func jsonEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}

func mapPendingAction(row dbgen.PendingAction) PendingAction {
	return PendingAction{
		ID: uuid.UUID(row.ID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(),
		Kind: ActionKind(row.Kind), TargetKind: ActionTargetKind(row.TargetKind), TargetID: uuid.UUID(row.TargetID.Bytes).String(),
		DesiredState: append(json.RawMessage(nil), row.DesiredState...), AuthoritativeState: append(json.RawMessage(nil), row.AuthoritativeState...),
		Status: ActionStatus(row.Status), Attempts: int(row.Attempts), MaxAttempts: int(row.MaxAttempts),
		AvailableAt: row.AvailableAt.Time.UTC(), LastErrorCode: textPointer(row.LastErrorCode),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}
