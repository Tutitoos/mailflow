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
	Locations   []RemoteLocation
	Content     NormalizedMessageContent
}

// RemoteLocation identifies the current provider location of a stable message.
// IMAP UIDs are deliberately kept out of RemoteID because COPY and MOVE may
// replace them while the RFC message identity remains unchanged.
type RemoteLocation struct {
	MailboxID   string
	UIDValidity int64
	UID         int64
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
	Messages          []RemoteMessage
	DeletedRemoteIDs  []string
	LocationSnapshots []RemoteLocationSnapshot
	NextCursor        SyncCursor
	HasMore           bool
}

// RemoteLocationSnapshot is emitted only after a complete provider-folder
// scan. The page writer removes stale IMAP locations in the same transaction
// that advances the sync checkpoint.
type RemoteLocationSnapshot struct {
	MailboxID   string
	UIDValidity int64
	PresentUIDs []int64
}

type RemoteAction struct {
	IdempotencyKey string
	Kind           string
	TargetKind     string
	TargetIDs      []string
	LabelIDs       []string
}

type OutgoingMessage struct {
	DraftID         string
	ThreadID        string
	SourceMessageID string
	Mode            ComposeMode
	Raw             io.Reader
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

// AccountProvider is the complete mail surface exposed by a connected account.
// Provider-specific implementations stay behind this interface so callers never
// select a provider from untrusted request data alone.
type AccountProvider interface {
	Provider
	AttachmentProvider
	DeleteDraft(context.Context, string) error
}
