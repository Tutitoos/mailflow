package sync

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/microsoftgraph"
	"github.com/jackc/pgx/v5"
)

const microsoftPageSize = 100

type MicrosoftProvider interface {
	mail.Provider
	mail.AttachmentProvider
}

type MicrosoftProviderResolver interface {
	ResolveMicrosoft(context.Context, string, string) (MicrosoftProvider, error)
}

type MicrosoftPageWriter interface {
	ApplyRemotePage(context.Context, pgx.Tx, string, string, mail.CatalogPage, mail.ChangePage) error
}

type MicrosoftExecutor struct {
	providers MicrosoftProviderResolver
	writer    MicrosoftPageWriter
	now       func() time.Time
}

type microsoftCheckpoint struct {
	History     mail.SyncCursor `json:"history"`
	Catalog     mail.SyncCursor `json:"catalog"`
	Page        mail.SyncCursor `json:"page"`
	Before      *time.Time      `json:"before,omitempty"`
	FolderIDs   []string        `json:"folderIds,omitempty"`
	CatalogDone bool            `json:"catalogDone"`
}

func NewMicrosoftExecutor(providers MicrosoftProviderResolver, writer MicrosoftPageWriter) (*MicrosoftExecutor, error) {
	if providers == nil || writer == nil {
		return nil, errors.New("Microsoft sync requires a provider resolver and page writer")
	}
	return &MicrosoftExecutor{providers: providers, writer: writer, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (executor *MicrosoftExecutor) FetchPage(ctx context.Context, user string, run Run) (SyncPage, error) {
	provider, err := executor.providers.ResolveMicrosoft(ctx, user, run.AccountID)
	if err != nil {
		return SyncPage{}, err
	}
	checkpoint, err := decodeMicrosoftCheckpoint(run.Checkpoint)
	if err != nil {
		return SyncPage{}, err
	}
	switch run.Phase {
	case PhaseRecent:
		if run.WindowStart == nil {
			return SyncPage{}, ErrInvalidRun
		}
		if checkpoint.History.Kind == "" {
			profile, profileErr := provider.Profile(ctx)
			if profileErr != nil || profile.History.Kind != "microsoft_delta" {
				return SyncPage{}, firstError(profileErr, ErrInvalidRun)
			}
			checkpoint.History = profile.History
			checkpoint.Before = run.WindowStart
		}
		if !checkpoint.CatalogDone {
			return executor.fetchCatalog(ctx, provider, user, run, checkpoint)
		}
		return executor.fetchBackfill(ctx, provider, user, run, checkpoint, run.WindowStart, nil)
	case PhaseHistorical:
		if checkpoint.Before == nil || checkpoint.History.Kind != "microsoft_delta" || !checkpoint.CatalogDone {
			return SyncPage{}, ErrInvalidRun
		}
		return executor.fetchBackfill(ctx, provider, user, run, checkpoint, nil, checkpoint.Before)
	case PhaseIncremental:
		if checkpoint.History.Kind != "microsoft_delta" || !checkpoint.CatalogDone {
			return SyncPage{}, ErrInvalidRun
		}
		if len(checkpoint.History.Value) == 0 {
			checkpoint.History, err = microsoftgraph.NewDeltaCursor(checkpoint.FolderIDs)
			if err != nil {
				return SyncPage{}, ErrInvalidRun
			}
		}
		page, changeErr := provider.Changes(ctx, checkpoint.History)
		if errors.Is(changeErr, microsoftgraph.ErrDeltaExpired) || errors.Is(changeErr, microsoftgraph.ErrInvalidCursor) {
			return SyncPage{}, ErrRemoteCursorInvalid
		}
		if changeErr != nil {
			return SyncPage{}, changeErr
		}
		checkpoint.History = page.NextCursor
		return executor.result(user, run.AccountID, checkpoint, mail.CatalogPage{}, page)
	case PhaseReconcile:
		if checkpoint.Before == nil {
			window := executor.now().Add(-RecentWindow)
			checkpoint.Before = &window
		}
		if !checkpoint.CatalogDone {
			return executor.fetchCatalog(ctx, provider, user, run, checkpoint)
		}
		return executor.fetchBackfill(ctx, provider, user, run, checkpoint, checkpoint.Before, nil)
	default:
		return SyncPage{}, ErrInvalidRun
	}
}

func (executor *MicrosoftExecutor) fetchCatalog(ctx context.Context, provider MicrosoftProvider, user string, run Run, checkpoint microsoftCheckpoint) (SyncPage, error) {
	catalog, err := provider.Catalog(ctx, checkpoint.Catalog)
	if err != nil {
		return SyncPage{}, err
	}
	for _, mailbox := range catalog.Mailboxes {
		checkpoint.FolderIDs = appendUnique(checkpoint.FolderIDs, mailbox.RemoteID)
	}
	if len(checkpoint.FolderIDs) > 512 {
		return SyncPage{}, ErrInvalidRun
	}
	checkpoint.Catalog = catalog.NextCursor
	checkpoint.CatalogDone = !catalog.HasMore
	if checkpoint.CatalogDone {
		checkpoint.Catalog = mail.SyncCursor{}
		if run.Phase == PhaseRecent {
			checkpoint.History, err = microsoftgraph.NewDeltaCursor(checkpoint.FolderIDs)
			if err != nil {
				return SyncPage{}, ErrInvalidRun
			}
		}
	}
	return executor.result(user, run.AccountID, checkpoint, catalog, mail.ChangePage{HasMore: true})
}

func (executor *MicrosoftExecutor) fetchBackfill(ctx context.Context, provider MicrosoftProvider, user string, run Run, checkpoint microsoftCheckpoint, after, before *time.Time) (SyncPage, error) {
	page, err := provider.Backfill(ctx, checkpoint.Page, after, before, microsoftPageSize)
	if err != nil {
		return SyncPage{}, err
	}
	if page.HasMore {
		checkpoint.Page = page.NextCursor
	} else {
		checkpoint.Page = mail.SyncCursor{}
	}
	return executor.result(user, run.AccountID, checkpoint, mail.CatalogPage{}, page)
}

func (executor *MicrosoftExecutor) result(user, account string, checkpoint microsoftCheckpoint, catalog mail.CatalogPage, page mail.ChangePage) (SyncPage, error) {
	encoded, err := json.Marshal(checkpoint)
	if err != nil || len(encoded) > 65536 {
		return SyncPage{}, ErrInvalidRun
	}
	applied := len(catalog.Mailboxes) + len(catalog.Labels) + len(page.Messages) + len(page.DeletedRemoteIDs)
	return SyncPage{
		Provider:     mail.ProviderMicrosoft,
		Checkpoint:   encoded,
		AppliedCount: int64(applied),
		HasMore:      page.HasMore,
		Apply: func(applyContext context.Context, tx pgx.Tx) error {
			return executor.writer.ApplyRemotePage(applyContext, tx, user, account, catalog, page)
		},
	}, nil
}

func decodeMicrosoftCheckpoint(value json.RawMessage) (microsoftCheckpoint, error) {
	if len(value) == 0 || string(value) == "{}" {
		return microsoftCheckpoint{}, nil
	}
	var checkpoint microsoftCheckpoint
	if len(value) > 65536 || json.Unmarshal(value, &checkpoint) != nil || len(checkpoint.FolderIDs) > 512 {
		return microsoftCheckpoint{}, ErrInvalidRun
	}
	seen := make(map[string]struct{}, len(checkpoint.FolderIDs))
	for _, id := range checkpoint.FolderIDs {
		if id == "" || len(id) > 512 {
			return microsoftCheckpoint{}, ErrInvalidRun
		}
		if _, exists := seen[id]; exists {
			return microsoftCheckpoint{}, ErrInvalidRun
		}
		seen[id] = struct{}{}
	}
	if checkpoint.CatalogDone && checkpoint.Catalog.Kind != "" {
		return microsoftCheckpoint{}, ErrInvalidRun
	}
	return checkpoint, nil
}

func appendUnique(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func firstError(candidate, fallback error) error {
	if candidate != nil {
		return candidate
	}
	return fallback
}
