package mail

import (
	"context"
	"errors"
	"time"
)

const (
	DraftLocalDebounce  = 2 * time.Second
	DraftRemoteInterval = 15 * time.Second
)

type DraftSyncStatus string

const (
	DraftQueued    DraftSyncStatus = "queued"
	DraftSyncing   DraftSyncStatus = "syncing"
	DraftSynced    DraftSyncStatus = "synced"
	DraftConflict  DraftSyncStatus = "conflict"
	DraftDiscarded DraftSyncStatus = "discarded"
)

var (
	ErrInvalidDraft  = errors.New("invalid draft")
	ErrDraftNotFound = errors.New("draft not found")
	ErrDraftConflict = errors.New("draft revision conflict")
)

type DraftRecipient struct {
	Role        AddressRole `json:"role"`
	Position    int32       `json:"position"`
	DisplayName *string     `json:"displayName"`
	Address     string      `json:"address"`
}

type DraftAttachment struct {
	Position  int32   `json:"position"`
	ObjectID  string  `json:"objectId"`
	Filename  *string `json:"filename"`
	MediaType string  `json:"mediaType"`
	SizeBytes int64   `json:"sizeBytes"`
}

type Draft struct {
	ID                 string            `json:"id"`
	AccountID          string            `json:"accountId"`
	RemoteID           *string           `json:"remoteId"`
	RemoteRevision     *string           `json:"remoteRevision"`
	Subject            string            `json:"subject"`
	BodyText           string            `json:"bodyText"`
	BodyHTML           string            `json:"bodyHtml"`
	Recipients         []DraftRecipient  `json:"recipients"`
	Attachments        []DraftAttachment `json:"attachments"`
	LocalRevision      int64             `json:"localRevision"`
	SyncedRevision     int64             `json:"syncedRevision"`
	SyncStatus         DraftSyncStatus   `json:"syncStatus"`
	RemoteCheckpointAt time.Time         `json:"remoteCheckpointAt"`
	LastRemoteSyncedAt *time.Time        `json:"lastRemoteSyncedAt"`
	DiscardedAt        *time.Time        `json:"discardedAt"`
	CreatedAt          time.Time         `json:"createdAt"`
	UpdatedAt          time.Time         `json:"updatedAt"`
}

type DraftContentInput struct {
	Subject     string
	BodyText    string
	BodyHTML    string
	Recipients  []MessageAddressInput
	Attachments []DraftAttachmentInput
}

type DraftAttachmentInput struct {
	ObjectID  string
	Filename  string
	MediaType string
	SizeBytes int64
}

type CreateDraftInput struct {
	UserID    string
	AccountID string
	Content   DraftContentInput
	Now       time.Time
}

type UpdateDraftInput struct {
	UserID           string
	AccountID        string
	DraftID          string
	ExpectedRevision int64
	Content          DraftContentInput
	Now              time.Time
}

type RemoteDraftCheckpoint struct {
	UserID         string
	AccountID      string
	DraftID        string
	LocalRevision  int64
	RemoteID       string
	RemoteRevision string
}

type DraftSchedule struct {
	LocalSaveAt        time.Time `json:"localSaveAt"`
	RemoteCheckpointAt time.Time `json:"remoteCheckpointAt"`
}

func ScheduleDraftAutosave(now time.Time) (DraftSchedule, error) {
	if now.IsZero() {
		return DraftSchedule{}, ErrInvalidDraft
	}
	now = now.UTC()
	return DraftSchedule{LocalSaveAt: now.Add(DraftLocalDebounce), RemoteCheckpointAt: now.Add(DraftRemoteInterval)}, nil
}

type DraftRepository interface {
	CreateDraft(context.Context, CreateDraftInput) (Draft, error)
	GetDraft(context.Context, string, string, string) (Draft, error)
	UpdateDraft(context.Context, UpdateDraftInput) (Draft, error)
	CheckpointRemote(context.Context, RemoteDraftCheckpoint) (Draft, error)
	MarkDraftConflict(context.Context, string, string, string, string, string) (Draft, error)
	DiscardDraft(context.Context, string, string, string) (Draft, error)
}
