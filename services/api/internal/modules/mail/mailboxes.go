package mail

import (
	"context"
	"errors"
	"time"
)

type MailboxRole string

const (
	MailboxInbox   MailboxRole = "inbox"
	MailboxSent    MailboxRole = "sent"
	MailboxDrafts  MailboxRole = "drafts"
	MailboxTrash   MailboxRole = "trash"
	MailboxJunk    MailboxRole = "junk"
	MailboxArchive MailboxRole = "archive"
	MailboxAll     MailboxRole = "all"
)

type LabelKind string
type Category string

const (
	LabelSystem   LabelKind = "system"
	LabelUser     LabelKind = "user"
	LabelCategory LabelKind = "category"

	CategoryPrimary       Category = "primary"
	CategoryPromotions    Category = "promotions"
	CategorySocial        Category = "social"
	CategoryNotifications Category = "notifications"
	CategoryForums        Category = "forums"
)

var (
	ErrInvalidMailbox  = errors.New("invalid mailbox")
	ErrMailboxNotFound = errors.New("mailbox not found")
	ErrInvalidLabel    = errors.New("invalid label")
	ErrLabelNotFound   = errors.New("label not found")
)

type Mailbox struct {
	ID             string      `json:"id"`
	AccountID      string      `json:"accountId"`
	RemoteID       string      `json:"remoteId"`
	RemoteName     string      `json:"remoteName"`
	LocalName      *string     `json:"localName"`
	Role           MailboxRole `json:"role"`
	Selectable     bool        `json:"selectable"`
	TotalCount     int32       `json:"totalCount"`
	UnreadCount    int32       `json:"unreadCount"`
	RemoteRevision *string     `json:"remoteRevision"`
	LastSyncedAt   *time.Time  `json:"lastSyncedAt"`
	CreatedAt      time.Time   `json:"createdAt"`
	UpdatedAt      time.Time   `json:"updatedAt"`
}

func (mailbox Mailbox) DisplayName() string {
	if mailbox.LocalName != nil {
		return *mailbox.LocalName
	}
	return mailbox.RemoteName
}

type Label struct {
	ID             string     `json:"id"`
	AccountID      string     `json:"accountId"`
	RemoteID       *string    `json:"remoteId"`
	RemoteName     string     `json:"remoteName"`
	LocalName      *string    `json:"localName"`
	Kind           LabelKind  `json:"kind"`
	Category       *Category  `json:"category"`
	Color          *string    `json:"color"`
	TotalCount     int32      `json:"totalCount"`
	UnreadCount    int32      `json:"unreadCount"`
	RemoteRevision *string    `json:"remoteRevision"`
	LastSyncedAt   *time.Time `json:"lastSyncedAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

func (label Label) DisplayName() string {
	if label.LocalName != nil {
		return *label.LocalName
	}
	return label.RemoteName
}

type ReconcileMailboxInput struct {
	UserID         string
	AccountID      string
	RemoteID       string
	RemoteName     string
	Role           MailboxRole
	Selectable     bool
	TotalCount     int32
	UnreadCount    int32
	RemoteRevision string
	LastSyncedAt   *time.Time
}

type ReconcileProviderLabelInput struct {
	UserID         string
	AccountID      string
	RemoteID       string
	RemoteName     string
	Kind           LabelKind
	Color          string
	TotalCount     int32
	UnreadCount    int32
	RemoteRevision string
	LastSyncedAt   *time.Time
}

type EnsureCategoryLabelInput struct {
	UserID    string
	AccountID string
	Category  Category
	Name      string
	Color     string
}

type MailboxLabelRepository interface {
	ReconcileMailbox(context.Context, ReconcileMailboxInput) (Mailbox, error)
	ListMailboxes(context.Context, string, string) ([]Mailbox, error)
	RenameMailbox(context.Context, string, string, string, string) (Mailbox, error)
	UpdateMailboxCounters(context.Context, string, string, string, int32, int32) (Mailbox, error)
	ReconcileProviderLabel(context.Context, ReconcileProviderLabelInput) (Label, error)
	EnsureCategoryLabel(context.Context, EnsureCategoryLabelInput) (Label, error)
	ListLabels(context.Context, string, string) ([]Label, error)
	RenameLabel(context.Context, string, string, string, string) (Label, error)
	UpdateLabelCounters(context.Context, string, string, string, int32, int32) (Label, error)
}
