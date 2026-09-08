package imap

import (
	"context"
	"errors"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

var (
	ErrInvalidCursor          = errors.New("IMAP synchronization cursor is invalid")
	ErrUIDValidityChanged     = errors.New("IMAP UIDVALIDITY changed")
	ErrInvalidMessageLocation = errors.New("IMAP message location is invalid")
	ErrMessagePersistence     = errors.New("IMAP message location could not be persisted")
	ErrActionUnsupported      = errors.New("IMAP action is unsupported")
	ErrDeliveryAmbiguous      = errors.New("SMTP delivery outcome is uncertain")
)

type FetchRequest struct {
	Folder   FolderState
	FromUID  int64
	After    *time.Time
	Before   *time.Time
	Limit    int
	Snapshot bool
}

type FetchedMessage struct {
	UID         int64
	UIDValidity int64
	SentAt      time.Time
	Flags       []string
	Raw         []byte
}

type FetchResult struct {
	Messages     []FetchedMessage
	NextUID      int64
	HasMore      bool
	SnapshotUIDs []int64
}

type AppendResult struct {
	UIDValidity int64
	UID         int64
}

type MoveResult struct {
	UIDValidity int64
	UID         int64
}

type Protocol interface {
	Fetch(context.Context, storedCredentials, FetchRequest) (FetchResult, error)
	SetFlags(context.Context, storedCredentials, MessageLocation, []string, []string) error
	Move(context.Context, storedCredentials, MessageLocation, FolderState, map[string]bool) (MoveResult, error)
	Append(context.Context, storedCredentials, FolderState, []byte, []string) (AppendResult, error)
	MarkDeleted(context.Context, storedCredentials, MessageLocation) error
	RawMessage(context.Context, storedCredentials, MessageLocation) ([]byte, error)
	SendSMTP(context.Context, storedCredentials, []byte) error
}

type MessageNormalizer interface {
	mail.MIMEMessageNormalizer
}
