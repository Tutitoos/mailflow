package sync

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type CursorKind string
type CursorState string

const (
	CursorGoogleHistory  CursorKind = "google_history"
	CursorMicrosoftDelta CursorKind = "microsoft_delta"
	CursorIMAPUID        CursorKind = "imap_uid"

	CursorActive         CursorState = "active"
	CursorResyncRequired CursorState = "resync_required"

	ReasonRemoteCursorInvalid = "remote_cursor_invalid"
	ReasonUIDValidityChanged  = "uid_validity_changed"
	ReasonManualReset         = "manual_reset"
)

var (
	ErrInvalidCursor  = errors.New("invalid sync cursor")
	ErrCursorNotFound = errors.New("sync cursor not found")
	ErrCursorExists   = errors.New("sync cursor already exists")
	ErrCursorConflict = errors.New("sync cursor version conflict")
	ErrResyncRequired = errors.New("account resynchronization required")
	ErrApplyChanges   = errors.New("provider changes could not be persisted")
)

type Cursor struct {
	AccountID          string          `json:"accountId"`
	Kind               CursorKind      `json:"kind"`
	Value              json.RawMessage `json:"value"`
	State              CursorState     `json:"state"`
	Checkpoint         int64           `json:"checkpoint"`
	UIDValidity        *int64          `json:"uidValidity"`
	Version            int64           `json:"version"`
	LastSuccessAt      *time.Time      `json:"lastSuccessAt"`
	InvalidatedAt      *time.Time      `json:"invalidatedAt"`
	InvalidationReason *string         `json:"invalidationReason"`
	UpdatedAt          time.Time       `json:"updatedAt"`
}

type CreateCursorInput struct {
	UserID      string
	AccountID   string
	Kind        CursorKind
	Value       json.RawMessage
	UIDValidity *int64
}

type AdvanceCursorInput struct {
	UserID          string
	AccountID       string
	ExpectedVersion int64
	Value           json.RawMessage
	UIDValidity     *int64
}

type ApplyChanges func(context.Context, pgx.Tx) error

type CursorStore interface {
	Create(context.Context, CreateCursorInput) (Cursor, error)
	Get(context.Context, string, string) (Cursor, error)
	Advance(context.Context, AdvanceCursorInput, ApplyChanges) (Cursor, error)
	Invalidate(context.Context, string, string, string) (Cursor, error)
}
