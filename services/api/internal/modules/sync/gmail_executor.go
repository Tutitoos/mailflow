package sync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/gmail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/jackc/pgx/v5"
)

const gmailPageSize = 50

type GmailProvider interface {
	Profile(context.Context) (mail.ProviderProfile, error)
	Catalog(context.Context, mail.SyncCursor) (mail.CatalogPage, error)
	Changes(context.Context, mail.SyncCursor) (mail.ChangePage, error)
	Backfill(context.Context, mail.SyncCursor, *time.Time, *time.Time, int) (mail.ChangePage, error)
	Apply(context.Context, mail.RemoteAction) error
	SaveDraft(context.Context, mail.OutgoingMessage) (string, error)
	Send(context.Context, mail.OutgoingMessage) (string, error)
	DeleteDraft(context.Context, string) error
	DownloadAttachment(context.Context, string, string) (io.ReadCloser, error)
}

type GmailProviderResolver interface {
	ResolveGmail(context.Context, string, string) (GmailProvider, error)
}

type GmailPageWriter interface {
	ApplyGmailPage(context.Context, pgx.Tx, string, string, mail.CatalogPage, mail.ChangePage) error
}

type GmailExecutor struct {
	providers GmailProviderResolver
	writer    GmailPageWriter
	now       func() time.Time
}

type gmailCheckpoint struct {
	History mail.SyncCursor `json:"history"`
	Page    mail.SyncCursor `json:"page"`
	Before  *time.Time      `json:"before,omitempty"`
}

func NewGmailExecutor(providers GmailProviderResolver, writer GmailPageWriter) (*GmailExecutor, error) {
	if providers == nil || writer == nil {
		return nil, errors.New("Gmail sync requires a provider resolver and page writer")
	}
	return &GmailExecutor{providers: providers, writer: writer, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (executor *GmailExecutor) FetchPage(ctx context.Context, user string, run Run) (SyncPage, error) {
	provider, err := executor.providers.ResolveGmail(ctx, user, run.AccountID)
	if err != nil {
		return SyncPage{}, err
	}
	checkpoint, err := decodeGmailCheckpoint(run.Checkpoint)
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
			if profileErr != nil {
				return SyncPage{}, profileErr
			}
			checkpoint.History = profile.History
			checkpoint.Before = run.WindowStart
			catalog, err = provider.Catalog(ctx, mail.SyncCursor{})
			if err != nil {
				return SyncPage{}, err
			}
		}
		page, err = provider.Backfill(ctx, checkpoint.Page, run.WindowStart, nil, gmailPageSize)
	case PhaseHistorical:
		if checkpoint.Before == nil || checkpoint.History.Kind == "" {
			return SyncPage{}, ErrInvalidRun
		}
		page, err = provider.Backfill(ctx, checkpoint.Page, nil, checkpoint.Before, gmailPageSize)
	case PhaseIncremental:
		if checkpoint.History.Kind == "" {
			return SyncPage{}, ErrInvalidRun
		}
		page, err = provider.Changes(ctx, checkpoint.History)
		if errors.Is(err, gmail.ErrHistoryExpired) {
			return SyncPage{}, ErrRemoteCursorInvalid
		}
	case PhaseReconcile:
		if checkpoint.History.Kind == "" {
			profile, profileErr := provider.Profile(ctx)
			if profileErr != nil {
				return SyncPage{}, profileErr
			}
			checkpoint.History = profile.History
			window := executor.now().Add(-RecentWindow)
			checkpoint.Before = &window
			catalog, err = provider.Catalog(ctx, mail.SyncCursor{})
			if err != nil {
				return SyncPage{}, err
			}
		}
		page, err = provider.Backfill(ctx, checkpoint.Page, checkpoint.Before, nil, gmailPageSize)
	default:
		return SyncPage{}, ErrInvalidRun
	}
	if err != nil {
		return SyncPage{}, err
	}
	if run.Phase == PhaseIncremental {
		checkpoint.History = page.NextCursor
	} else if page.HasMore {
		checkpoint.Page = page.NextCursor
	} else {
		checkpoint.Page = mail.SyncCursor{}
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return SyncPage{}, ErrInvalidRun
	}
	return SyncPage{
		Provider:     mail.ProviderGoogle,
		Checkpoint:   encoded,
		AppliedCount: int64(len(page.Messages) + len(page.DeletedRemoteIDs)),
		HasMore:      page.HasMore,
		Apply: func(applyContext context.Context, tx pgx.Tx) error {
			return executor.writer.ApplyGmailPage(applyContext, tx, user, run.AccountID, catalog, page)
		},
	}, nil
}

func decodeGmailCheckpoint(value json.RawMessage) (gmailCheckpoint, error) {
	if len(value) == 0 || string(value) == "{}" {
		return gmailCheckpoint{}, nil
	}
	var checkpoint gmailCheckpoint
	if len(value) > 65536 || json.Unmarshal(value, &checkpoint) != nil {
		return gmailCheckpoint{}, ErrInvalidRun
	}
	return checkpoint, nil
}
