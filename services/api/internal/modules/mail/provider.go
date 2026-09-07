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
	RemoteID    string
	ThreadID    string
	SentAt      time.Time
	IsRead      bool
	IsStarred   bool
	IsImportant bool
	Category    Category
	InTrash     bool
	LabelIDs    []string
	Content     NormalizedMessageContent
}

type ProviderProfile struct {
	RemoteID string
	Address  string
	History  SyncCursor
}

type RemoteMailbox struct {
	RemoteID    string
	Name        string
	Role        MailboxRole
	Selectable  bool
	TotalCount  int32
	UnreadCount int32
}

type RemoteLabel struct {
	RemoteID    string
	Name        string
	Kind        LabelKind
	Category    *Category
	TotalCount  int32
	UnreadCount int32
}

type CatalogPage struct {
	Mailboxes  []RemoteMailbox
	Labels     []RemoteLabel
	NextCursor SyncCursor
	HasMore    bool
}

type ChangePage struct {
	Messages         []RemoteMessage
	DeletedRemoteIDs []string
	NextCursor       SyncCursor
	HasMore          bool
}

type RemoteAction struct {
	IdempotencyKey string
	Kind           string
	TargetKind     string
	TargetIDs      []string
	LabelIDs       []string
}

type OutgoingMessage struct {
	DraftID string
	Raw     io.Reader
}

type Provider interface {
	Kind() ProviderKind
	Capabilities(ctx context.Context) (map[string]bool, error)
	Changes(ctx context.Context, cursor SyncCursor) (ChangePage, error)
	Profile(ctx context.Context) (ProviderProfile, error)
	Catalog(ctx context.Context, cursor SyncCursor) (CatalogPage, error)
	Backfill(ctx context.Context, cursor SyncCursor, after, before *time.Time, limit int) (ChangePage, error)
	Apply(ctx context.Context, action RemoteAction) error
	SaveDraft(ctx context.Context, draft OutgoingMessage) (string, error)
	Send(ctx context.Context, message OutgoingMessage) (string, error)
}

type AttachmentProvider interface {
	DownloadAttachment(ctx context.Context, messageID, attachmentID string) (io.ReadCloser, error)
}
