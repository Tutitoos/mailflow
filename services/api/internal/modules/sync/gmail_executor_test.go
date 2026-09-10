package sync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/gmail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/jackc/pgx/v5"
)

type fakeGmailResolver struct{ provider *fakeGmailProvider }

func (resolver fakeGmailResolver) ResolveGmail(context.Context, string, string) (GmailProvider, error) {
	return resolver.provider, nil
}

type fakeGmailProvider struct {
	historyExpired bool
	after          *time.Time
	before         *time.Time
	backfills      int
	limit          int
}

func (*fakeGmailProvider) Profile(context.Context) (mail.ProviderProfile, error) {
	return mail.ProviderProfile{History: mail.SyncCursor{Kind: "google_history", Value: []byte(`{"historyId":"10"}`)}}, nil
}
func (*fakeGmailProvider) Catalog(context.Context, mail.SyncCursor) (mail.CatalogPage, error) {
	return mail.CatalogPage{Mailboxes: []mail.RemoteMailbox{{RemoteID: "INBOX", Name: "Inbox", Role: mail.MailboxInbox, Selectable: true}}}, nil
}
func (provider *fakeGmailProvider) Changes(context.Context, mail.SyncCursor) (mail.ChangePage, error) {
	if provider.historyExpired {
		return mail.ChangePage{}, gmail.ErrHistoryExpired
	}
	return mail.ChangePage{Messages: []mail.RemoteMessage{{RemoteID: "changed"}}, NextCursor: mail.SyncCursor{Kind: "google_history", Value: []byte(`{"historyId":"11"}`)}}, nil
}
func (provider *fakeGmailProvider) Backfill(_ context.Context, cursor mail.SyncCursor, after, before *time.Time, limit int) (mail.ChangePage, error) {
	provider.after, provider.before = after, before
	provider.limit = limit
	provider.backfills++
	if len(cursor.Value) == 0 && provider.backfills == 1 {
		return mail.ChangePage{Messages: []mail.RemoteMessage{{RemoteID: "recent"}}, NextCursor: mail.SyncCursor{Kind: "google_backfill", Value: []byte("page-2")}, HasMore: true}, nil
	}
	return mail.ChangePage{Messages: []mail.RemoteMessage{{RemoteID: "last"}}}, nil
}
func (*fakeGmailProvider) Apply(context.Context, mail.RemoteAction) error { return nil }
func (*fakeGmailProvider) SaveDraft(context.Context, mail.OutgoingMessage) (string, error) {
	return "draft", nil
}
func (*fakeGmailProvider) Send(context.Context, mail.OutgoingMessage) (string, error) {
	return "message", nil
}
func (*fakeGmailProvider) DeleteDraft(context.Context, string) error { return nil }
func (*fakeGmailProvider) DownloadAttachment(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("attachment")), nil
}

type fakeGmailWriter struct {
	catalogs int
	messages int
}

func (writer *fakeGmailWriter) ApplyGmailPage(_ context.Context, _ pgx.Tx, _, _ string, catalog mail.CatalogPage, page mail.ChangePage) error {
	writer.catalogs += len(catalog.Mailboxes) + len(catalog.Labels)
	writer.messages += len(page.Messages)
	return nil
}

func TestGmailExecutorCarriesSnapshotAcrossRecentHistoricalAndIncrementalPhases(t *testing.T) {
	provider, writer := &fakeGmailProvider{}, &fakeGmailWriter{}
	executor, err := NewGmailExecutor(fakeGmailResolver{provider}, writer)
	if err != nil {
		t.Fatal(err)
	}
	window := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	run := Run{AccountID: "0199ed3b-c950-7000-8000-000000000016", Phase: PhaseRecent, Checkpoint: json.RawMessage(`{}`), WindowStart: &window}
	first, err := executor.FetchPage(context.Background(), "0199ed3b-c950-7000-8000-000000000001", run)
	if err != nil || !first.HasMore || provider.after == nil || !provider.after.Equal(window) || provider.before != nil || provider.limit != 50 {
		t.Fatalf("first recent page = %+v, %v", first, err)
	}
	if err := first.Apply(context.Background(), nil); err != nil || writer.catalogs != 1 || writer.messages != 1 {
		t.Fatalf("first apply catalogs=%d messages=%d error=%v", writer.catalogs, writer.messages, err)
	}
	run.Checkpoint, run.Version = first.Checkpoint, 2
	second, err := executor.FetchPage(context.Background(), "0199ed3b-c950-7000-8000-000000000001", run)
	if err != nil || second.HasMore {
		t.Fatalf("second recent page = %+v, %v", second, err)
	}
	run.Phase, run.Checkpoint, run.WindowStart = PhaseHistorical, second.Checkpoint, nil
	historical, err := executor.FetchPage(context.Background(), "0199ed3b-c950-7000-8000-000000000001", run)
	if err != nil || provider.after != nil || provider.before == nil || !provider.before.Equal(window) {
		t.Fatalf("historical page = %+v, %v", historical, err)
	}
	run.Phase, run.Checkpoint = PhaseIncremental, historical.Checkpoint
	incremental, err := executor.FetchPage(context.Background(), "0199ed3b-c950-7000-8000-000000000001", run)
	if err != nil || incremental.AppliedCount != 1 {
		t.Fatalf("incremental page = %+v, %v", incremental, err)
	}
	var checkpoint gmailCheckpoint
	if json.Unmarshal(incremental.Checkpoint, &checkpoint) != nil || string(checkpoint.History.Value) != `{"historyId":"11"}` {
		t.Fatalf("incremental checkpoint = %s", incremental.Checkpoint)
	}
}

func TestGmailExecutorRequestsBoundedRecoveryForExpiredHistory(t *testing.T) {
	provider := &fakeGmailProvider{historyExpired: true}
	executor, _ := NewGmailExecutor(fakeGmailResolver{provider}, &fakeGmailWriter{})
	checkpoint, _ := json.Marshal(gmailCheckpoint{History: mail.SyncCursor{Kind: "google_history", Value: []byte(`{"historyId":"old"}`)}})
	_, err := executor.FetchPage(context.Background(), "0199ed3b-c950-7000-8000-000000000001", Run{AccountID: "0199ed3b-c950-7000-8000-000000000016", Phase: PhaseIncremental, Checkpoint: checkpoint})
	if !errors.Is(err, ErrRemoteCursorInvalid) {
		t.Fatalf("expired history error = %v", err)
	}
}
