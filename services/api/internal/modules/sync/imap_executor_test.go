package sync

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	mailflowimap "github.com/Tutitoos/mailflow/services/api/internal/modules/imap"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/jackc/pgx/v5"
)

type fakeIMAPProvider struct {
	profile       mail.ProviderProfile
	catalog       mail.CatalogPage
	backfill      mail.ChangePage
	changes       mail.ChangePage
	changesError  error
	backfillCalls int
}

func (*fakeIMAPProvider) Kind() mail.ProviderKind { return mail.ProviderIMAP }
func (*fakeIMAPProvider) Capabilities(context.Context) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (provider *fakeIMAPProvider) Profile(context.Context) (mail.ProviderProfile, error) {
	return provider.profile, nil
}
func (provider *fakeIMAPProvider) Catalog(context.Context, mail.SyncCursor) (mail.CatalogPage, error) {
	return provider.catalog, nil
}
func (provider *fakeIMAPProvider) Backfill(context.Context, mail.SyncCursor, *time.Time, *time.Time, int) (mail.ChangePage, error) {
	provider.backfillCalls++
	return provider.backfill, nil
}
func (provider *fakeIMAPProvider) Changes(context.Context, mail.SyncCursor) (mail.ChangePage, error) {
	return provider.changes, provider.changesError
}
func (*fakeIMAPProvider) Apply(context.Context, mail.RemoteAction) error { return nil }
func (*fakeIMAPProvider) SaveDraft(context.Context, mail.OutgoingMessage) (string, error) {
	return "draft", nil
}
func (*fakeIMAPProvider) Send(context.Context, mail.OutgoingMessage) (string, error) {
	return "sent", nil
}
func (*fakeIMAPProvider) DeleteDraft(context.Context, string) error { return nil }
func (*fakeIMAPProvider) DownloadAttachment(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("fixture")), nil
}

type fakeIMAPResolver struct{ provider IMAPProvider }

func (resolver fakeIMAPResolver) ResolveIMAP(context.Context, string, string) (IMAPProvider, error) {
	return resolver.provider, nil
}

type fakeIMAPWriter struct{ applications int }

func (writer *fakeIMAPWriter) ApplyRemotePage(context.Context, pgx.Tx, string, string, mail.CatalogPage, mail.ChangePage) error {
	writer.applications++
	return nil
}

func TestIMAPExecutorPersistsCatalogBeforeRecentBackfill(t *testing.T) {
	history := mail.SyncCursor{Kind: "imap_uid", Value: []byte(`{"position":0,"folders":[{"remoteId":"inbox","uidValidity":1,"nextUid":4}]}`)}
	provider := &fakeIMAPProvider{
		profile:  mail.ProviderProfile{History: history},
		catalog:  mail.CatalogPage{Mailboxes: []mail.RemoteMailbox{{RemoteID: "inbox", Name: "Inbox"}}},
		backfill: mail.ChangePage{Messages: []mail.RemoteMessage{{RemoteID: "message"}}},
	}
	writer := &fakeIMAPWriter{}
	executor, err := NewIMAPExecutor(fakeIMAPResolver{provider}, writer)
	if err != nil {
		t.Fatal(err)
	}
	window := time.Now().UTC().Add(-90 * 24 * time.Hour)
	first, err := executor.FetchPage(context.Background(), "owner", Run{AccountID: "account", Phase: PhaseRecent, WindowStart: &window})
	if err != nil || !first.HasMore || first.AppliedCount != 1 || provider.backfillCalls != 0 {
		t.Fatalf("catalog page=%+v calls=%d error=%v", first, provider.backfillCalls, err)
	}
	if err := first.Apply(context.Background(), nil); err != nil || writer.applications != 1 {
		t.Fatalf("catalog apply count=%d error=%v", writer.applications, err)
	}
	second, err := executor.FetchPage(context.Background(), "owner", Run{AccountID: "account", Phase: PhaseRecent, WindowStart: &window, Checkpoint: first.Checkpoint})
	if err != nil || second.AppliedCount != 1 || provider.backfillCalls != 1 {
		t.Fatalf("backfill page=%+v calls=%d error=%v", second, provider.backfillCalls, err)
	}
}

func TestIMAPExecutorMapsUIDValidityInvalidationToRecovery(t *testing.T) {
	provider := &fakeIMAPProvider{changesError: mailflowimap.ErrUIDValidityChanged}
	executor, _ := NewIMAPExecutor(fakeIMAPResolver{provider}, &fakeIMAPWriter{})
	checkpoint := []byte(`{"history":{"Kind":"imap_uid","Value":"e30="},"catalogDone":true}`)
	_, err := executor.FetchPage(context.Background(), "owner", Run{AccountID: "account", Phase: PhaseIncremental, Checkpoint: checkpoint})
	if !errors.Is(err, ErrRemoteCursorInvalid) {
		t.Fatalf("incremental recovery error = %v", err)
	}
}
