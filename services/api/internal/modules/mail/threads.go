package mail

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidThread   = errors.New("invalid thread")
	ErrThreadNotFound  = errors.New("thread not found")
	ErrInvalidMessage  = errors.New("invalid message")
	ErrMessageNotFound = errors.New("message not found")
)

type Thread struct {
	ID            string     `json:"id"`
	AccountID     string     `json:"accountId"`
	RemoteID      string     `json:"remoteId"`
	LastMessageAt time.Time  `json:"lastMessageAt"`
	IsRead        bool       `json:"isRead"`
	IsStarred     bool       `json:"isStarred"`
	IsImportant   bool       `json:"isImportant"`
	Category      Category   `json:"category"`
	DeletedAt     *time.Time `json:"deletedAt"`
	MessageCount  int32      `json:"messageCount"`
	UnreadCount   int32      `json:"unreadCount"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

type AddressRole string

const (
	AddressFrom    AddressRole = "from"
	AddressSender  AddressRole = "sender"
	AddressReplyTo AddressRole = "reply_to"
	AddressTo      AddressRole = "to"
	AddressCC      AddressRole = "cc"
	AddressBCC     AddressRole = "bcc"
)

type MessageAddress struct {
	Role        AddressRole `json:"role"`
	Position    int32       `json:"position"`
	DisplayName *string     `json:"displayName"`
	Address     string      `json:"address"`
}

type Message struct {
	ID          string           `json:"id"`
	ThreadID    string           `json:"threadId"`
	AccountID   string           `json:"accountId"`
	RemoteID    string           `json:"remoteId"`
	MessageID   *string          `json:"messageId"`
	References  []string         `json:"references"`
	SentAt      time.Time        `json:"sentAt"`
	IsRead      bool             `json:"isRead"`
	IsStarred   bool             `json:"isStarred"`
	IsImportant bool             `json:"isImportant"`
	DeletedAt   *time.Time       `json:"deletedAt"`
	Addresses   []MessageAddress `json:"addresses"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
}

type UpsertThreadInput struct {
	UserID        string
	AccountID     string
	RemoteID      string
	LastMessageAt time.Time
	IsRead        bool
	IsStarred     bool
	IsImportant   bool
	Category      Category
	DeletedAt     *time.Time
}

type MessageAddressInput struct {
	Role        AddressRole
	DisplayName string
	Address     string
}

type UpsertMessageInput struct {
	UserID      string
	AccountID   string
	ThreadID    string
	RemoteID    string
	MessageID   string
	References  []string
	SentAt      time.Time
	IsRead      bool
	IsStarred   bool
	IsImportant bool
	DeletedAt   *time.Time
	Addresses   []MessageAddressInput
}

type StatePatch struct {
	Read      *bool
	Starred   *bool
	Important *bool
	Deleted   *bool
}

type ThreadCursor struct {
	LastMessageAt time.Time `json:"lastMessageAt"`
	ID            string    `json:"id"`
}

type ThreadPage struct {
	Items []Thread      `json:"items"`
	Next  *ThreadCursor `json:"next"`
}

type MessageCursor struct {
	SentAt time.Time `json:"sentAt"`
	ID     string    `json:"id"`
}

type MessagePage struct {
	Items []Message      `json:"items"`
	Next  *MessageCursor `json:"next"`
}

type ThreadMessageRepository interface {
	UpsertThread(context.Context, UpsertThreadInput) (Thread, error)
	GetThread(context.Context, string, string, string) (Thread, error)
	ListThreads(context.Context, string, string, *ThreadCursor, int) (ThreadPage, error)
	UpsertMessage(context.Context, UpsertMessageInput) (Message, error)
	ListMessages(context.Context, string, string, string, *MessageCursor, int) (MessagePage, error)
	ApplyThreadState(context.Context, string, string, string, StatePatch) (Thread, error)
	ApplyMessageState(context.Context, string, string, string, string, StatePatch) (Message, error)
}
