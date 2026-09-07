package mail

import (
	"context"
	"io"
	"time"
)

type ProviderKind string

const (
	ProviderGoogle    ProviderKind = "google"
	ProviderMicrosoft ProviderKind = "microsoft"
	ProviderIMAP      ProviderKind = "imap"
)

type SyncCursor struct {
	Kind  string
	Value []byte
}

type RemoteMessage struct {
	RemoteID string
	ThreadID string
	SentAt   time.Time
	Content  NormalizedMessageContent
}

type ChangePage struct {
	Messages   []RemoteMessage
	NextCursor SyncCursor
	HasMore    bool
}

type RemoteAction struct {
	IdempotencyKey string
	Kind           string
	TargetIDs      []string
}

type OutgoingMessage struct {
	DraftID string
	Raw     io.Reader
}

type Provider interface {
	Kind() ProviderKind
	Capabilities(ctx context.Context) (map[string]bool, error)
	Changes(ctx context.Context, cursor SyncCursor) (ChangePage, error)
	Backfill(ctx context.Context, before time.Time, limit int) (ChangePage, error)
	Apply(ctx context.Context, action RemoteAction) error
	SaveDraft(ctx context.Context, draft OutgoingMessage) (string, error)
	Send(ctx context.Context, message OutgoingMessage) (string, error)
}
