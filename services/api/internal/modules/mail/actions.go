package mail

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/jackc/pgx/v5"
)

type ActionKind string
type ActionTargetKind string
type ActionStatus string

const (
	ActionMarkRead        ActionKind = "mark_read"
	ActionMarkUnread      ActionKind = "mark_unread"
	ActionStar            ActionKind = "star"
	ActionUnstar          ActionKind = "unstar"
	ActionMarkImportant   ActionKind = "mark_important"
	ActionMarkUnimportant ActionKind = "mark_unimportant"
	ActionMoveToTrash     ActionKind = "move_to_trash"
	ActionRestoreTrash    ActionKind = "restore_from_trash"
	ActionArchive         ActionKind = "archive"
	ActionAddLabel        ActionKind = "add_label"
	ActionRemoveLabel     ActionKind = "remove_label"

	ActionTargetThread  ActionTargetKind = "thread"
	ActionTargetMessage ActionTargetKind = "message"

	ActionPending    ActionStatus = "pending"
	ActionProcessing ActionStatus = "processing"
	ActionRetryWait  ActionStatus = "retry_wait"
	ActionCompleted  ActionStatus = "completed"
	ActionConflict   ActionStatus = "conflict"
)

var (
	ErrInvalidAction       = errors.New("invalid mail action")
	ErrActionNotFound      = errors.New("mail action not found")
	ErrActionUnavailable   = errors.New("no mail action available")
	ErrActionClaimLost     = errors.New("mail action claim was lost")
	ErrIdempotencyConflict = errors.New("idempotency key belongs to another mail action")
	ErrOptimisticUpdate    = errors.New("optimistic mail state could not be persisted")
	ErrAuthoritativeUpdate = errors.New("authoritative mail state could not be restored")
	ErrActionEvents        = errors.New("mail action events are unavailable")
)

type PendingAction struct {
	ID                 string           `json:"id"`
	AccountID          string           `json:"accountId"`
	Kind               ActionKind       `json:"kind"`
	TargetKind         ActionTargetKind `json:"targetKind"`
	TargetID           string           `json:"targetId"`
	DesiredState       json.RawMessage  `json:"desiredState"`
	AuthoritativeState json.RawMessage  `json:"authoritativeState,omitempty"`
	Status             ActionStatus     `json:"status"`
	Attempts           int              `json:"attempts"`
	MaxAttempts        int              `json:"maxAttempts"`
	AvailableAt        time.Time        `json:"availableAt"`
	LastErrorCode      *string          `json:"lastErrorCode"`
	CreatedAt          time.Time        `json:"createdAt"`
	UpdatedAt          time.Time        `json:"updatedAt"`
	IdempotencyKey     string           `json:"-"`
}

type ActionClaim struct {
	PendingAction
	token string
}

type EnqueueActionInput struct {
	UserID             string
	AccountID          string
	IdempotencyKey     string
	Kind               ActionKind
	TargetKind         ActionTargetKind
	TargetID           string
	DesiredState       json.RawMessage
	AuthoritativeState json.RawMessage
	MaxAttempts        int
}

type ApplyActionState func(context.Context, pgx.Tx) error

type PendingActionStore interface {
	Enqueue(context.Context, EnqueueActionInput, ApplyActionState) (PendingAction, bool, error)
	Claim(context.Context, string) (ActionClaim, error)
	Complete(context.Context, string, ActionClaim) (PendingAction, error)
	Fail(context.Context, string, ActionClaim, string, time.Time, json.RawMessage, ApplyActionState) (PendingAction, bool, error)
}

type ActionEventPublisher interface {
	Publish(context.Context, string, string, json.RawMessage) (events.Envelope, error)
}
