package sync

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	mailflowimap "github.com/Tutitoos/mailflow/services/api/internal/modules/imap"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/jackc/pgx/v5"
)

const imapSyncPageSize = 100

type IMAPPageWriter interface {
	ApplyRemotePage(context.Context, pgx.Tx, string, string, mail.CatalogPage, mail.ChangePage) error
}

type IMAPExecutor struct {
	providers IMAPProviderResolver
	writer    IMAPPageWriter
}

type imapCheckpoint struct {
	History     mail.SyncCursor `json:"history"`
	Page        mail.SyncCursor `json:"page"`
	Before      *time.Time      `json:"before,omitempty"`
	CatalogDone bool            `json:"catalogDone"`
}

func NewIMAPExecutor(providers IMAPProviderResolver, writer IMAPPageWriter) (*IMAPExecutor, error) {
	if providers == nil || writer == nil {
		return nil, errors.New("IMAP sync requires a provider resolver and page writer")
	}
	return &IMAPExecutor{providers: providers, writer: writer}, nil
}

func (executor *IMAPExecutor) FetchPage(ctx context.Context, user string, run Run) (SyncPage, error) {
	provider, err := executor.providers.ResolveIMAP(ctx, user, run.AccountID)
	if err != nil {
		return SyncPage{}, err
	}
	checkpoint, err := decodeIMAPCheckpoint(run.Checkpoint)
	if err != nil {
		return SyncPage{}, err
	}
	var catalog mail.CatalogPage
	var page mail.ChangePage
	switch run.Phase {
	case PhaseRecent:
		if run.WindowStart == nil {
			return SyncPage{}, ErrInvalidRun
		}
		if checkpoint.History.Kind == "" {
			profile, profileErr := provider.Profile(ctx)
			if profileErr != nil || profile.History.Kind != "imap_uid" {
				return SyncPage{}, firstError(profileErr, ErrInvalidRun)
			}
			checkpoint.History = profile.History
			checkpoint.Before = run.WindowStart
		}
		if !checkpoint.CatalogDone {
			catalog, err = provider.Catalog(ctx, mail.SyncCursor{})
			checkpoint.CatalogDone = err == nil
			if err == nil {
				page.HasMore = true
			}
		} else {
			page, err = provider.Backfill(ctx, checkpoint.Page, run.WindowStart, nil, imapSyncPageSize)
		}
	case PhaseHistorical:
		if checkpoint.Before == nil || checkpoint.History.Kind != "imap_uid" || !checkpoint.CatalogDone {
			return SyncPage{}, ErrInvalidRun
		}
		page, err = provider.Backfill(ctx, checkpoint.Page, nil, checkpoint.Before, imapSyncPageSize)
	case PhaseIncremental:
		if checkpoint.History.Kind != "imap_uid" || !checkpoint.CatalogDone {
			return SyncPage{}, ErrInvalidRun
		}
		page, err = provider.Changes(ctx, checkpoint.History)
		if errors.Is(err, mailflowimap.ErrUIDValidityChanged) || errors.Is(err, mailflowimap.ErrInvalidCursor) {
			return SyncPage{}, ErrRemoteCursorInvalid
		}
		checkpoint.History = page.NextCursor
	case PhaseReconcile:
		catalog, err = provider.Catalog(ctx, mail.SyncCursor{})
		if err == nil {
			checkpoint.CatalogDone = true
			page, err = provider.Backfill(ctx, checkpoint.Page, nil, nil, imapSyncPageSize)
		}
	default:
		return SyncPage{}, ErrInvalidRun
	}
	if err != nil {
		return SyncPage{}, err
	}
	if run.Phase != PhaseIncremental {
		if page.HasMore {
			checkpoint.Page = page.NextCursor
		} else {
			checkpoint.Page = mail.SyncCursor{}
		}
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil || len(encoded) > 65536 {
		return SyncPage{}, ErrInvalidRun
	}
	return SyncPage{
		Provider: mail.ProviderIMAP, Checkpoint: encoded,
		AppliedCount: int64(len(catalog.Mailboxes) + len(page.Messages) + len(page.DeletedRemoteIDs)), HasMore: page.HasMore,
		Apply: func(applyContext context.Context, tx pgx.Tx) error {
			return executor.writer.ApplyRemotePage(applyContext, tx, user, run.AccountID, catalog, page)
		},
	}, nil
}

func decodeIMAPCheckpoint(value json.RawMessage) (imapCheckpoint, error) {
	if len(value) == 0 || string(value) == "{}" {
		return imapCheckpoint{}, nil
	}
	var checkpoint imapCheckpoint
	if len(value) > 65536 || json.Unmarshal(value, &checkpoint) != nil {
		return imapCheckpoint{}, ErrInvalidRun
	}
	return checkpoint, nil
}
