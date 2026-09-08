package sync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/microsoftgraph"
	"github.com/jackc/pgx/v5"
)

type fakeMicrosoftResolver struct {
	provider *fakeMicrosoftProvider
	user     string
	account  string
}

func (resolver *fakeMicrosoftResolver) ResolveMicrosoft(_ context.Context, user, account string) (MicrosoftProvider, error) {
	resolver.user, resolver.account = user, account
	return resolver.provider, nil
}

type fakeMicrosoftProvider struct {
	catalogs     int
	backfills    int
	changes      int
	deltaExpired bool
	after        *time.Time
	before       *time.Time
}

func (*fakeMicrosoftProvider) Profile(context.Context) (mail.ProviderProfile, error) {
	return mail.ProviderProfile{History: mail.SyncCursor{Kind: "microsoft_delta"}}, nil
}

func (*fakeMicrosoftProvider) Kind() mail.ProviderKind { return mail.ProviderMicrosoft }

func (*fakeMicrosoftProvider) Capabilities(context.Context) (map[string]bool, error) {
	return map[string]bool{"folders": true}, nil
}

func (provider *fakeMicrosoftProvider) Catalog(_ context.Context, cursor mail.SyncCursor) (mail.CatalogPage, error) {
	provider.catalogs++
	if provider.catalogs == 1 {
		return mail.CatalogPage{Mailboxes: []mail.RemoteMailbox{{RemoteID: "folder-a", Name: "Inbox"}}, NextCursor: mail.SyncCursor{Kind: "microsoft_catalog", Value: []byte(`{"page":2}`)}, HasMore: true}, nil
	}
	if cursor.Kind != "microsoft_catalog" {
		return mail.CatalogPage{}, errors.New("missing catalog cursor")
	}
	return mail.CatalogPage{Mailboxes: []mail.RemoteMailbox{{RemoteID: "folder-b", Name: "Archive"}}}, nil
}

func (provider *fakeMicrosoftProvider) Changes(_ context.Context, cursor mail.SyncCursor) (mail.ChangePage, error) {
	provider.changes++
	if provider.deltaExpired {
		return mail.ChangePage{}, microsoftgraph.ErrDeltaExpired
	}
	if cursor.Kind != "microsoft_delta" || len(cursor.Value) == 0 {
		return mail.ChangePage{}, errors.New("missing delta cursor")
	}
	return mail.ChangePage{Messages: []mail.RemoteMessage{{RemoteID: "changed"}}, NextCursor: cursor}, nil
}

func (provider *fakeMicrosoftProvider) Backfill(_ context.Context, _ mail.SyncCursor, after, before *time.Time, _ int) (mail.ChangePage, error) {
	provider.backfills++
	provider.after, provider.before = after, before
	return mail.ChangePage{Messages: []mail.RemoteMessage{{RemoteID: "backfill"}}}, nil
}

func (*fakeMicrosoftProvider) Apply(context.Context, mail.RemoteAction) error { return nil }
func (*fakeMicrosoftProvider) SaveDraft(context.Context, mail.OutgoingMessage) (string, error) {
	return "draft", nil
}
func (*fakeMicrosoftProvider) Send(context.Context, mail.OutgoingMessage) (string, error) {
	return "message", nil
}
func (*fakeMicrosoftProvider) DownloadAttachment(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("attachment")), nil
}

type fakeMicrosoftWriter struct {
	catalogs int
	messages int
	deletes  int
}

func (writer *fakeMicrosoftWriter) ApplyRemotePage(_ context.Context, _ pgx.Tx, _, _ string, catalog mail.CatalogPage, page mail.ChangePage) error {
	writer.catalogs += len(catalog.Mailboxes) + len(catalog.Labels)
	writer.messages += len(page.Messages)
	writer.deletes += len(page.DeletedRemoteIDs)
	return nil
}

func TestMicrosoftExecutorCarriesCatalogBackfillAndDeltaCheckpoint(t *testing.T) {
	provider := &fakeMicrosoftProvider{}
	resolver := &fakeMicrosoftResolver{provider: provider}
	writer := &fakeMicrosoftWriter{}
	executor, err := NewMicrosoftExecutor(resolver, writer)
	if err != nil {
		t.Fatal(err)
	}
	user, account := "0199ed3b-c950-7000-8000-000000000001", "0199ed3b-c950-7000-8000-000000000016"
	window := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	run := Run{AccountID: account, Phase: PhaseRecent, Checkpoint: json.RawMessage(`{}`), WindowStart: &window}

	first, err := executor.FetchPage(context.Background(), user, run)
	if err != nil || !first.HasMore || first.Provider != mail.ProviderMicrosoft {
		t.Fatalf("first catalog = %+v, %v", first, err)
	}
	if err := first.Apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	run.Checkpoint = first.Checkpoint
	second, err := executor.FetchPage(context.Background(), user, run)
	if err != nil || !second.HasMore {
		t.Fatalf("second catalog = %+v, %v", second, err)
	}
	if err := second.Apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var checkpoint microsoftCheckpoint
	if json.Unmarshal(second.Checkpoint, &checkpoint) != nil || !checkpoint.CatalogDone || checkpoint.History.Kind != "microsoft_delta" || len(checkpoint.History.Value) == 0 || len(checkpoint.FolderIDs) != 2 {
		t.Fatalf("catalog checkpoint = %s", second.Checkpoint)
	}

	run.Checkpoint = second.Checkpoint
	recent, err := executor.FetchPage(context.Background(), user, run)
	if err != nil || recent.HasMore || provider.after == nil || !provider.after.Equal(window) || provider.before != nil {
		t.Fatalf("recent = %+v, %v", recent, err)
	}
	run.Phase, run.WindowStart, run.Checkpoint = PhaseHistorical, nil, recent.Checkpoint
	historical, err := executor.FetchPage(context.Background(), user, run)
	if err != nil || provider.after != nil || provider.before == nil || !provider.before.Equal(window) {
		t.Fatalf("historical = %+v, %v", historical, err)
	}
	run.Phase, run.Checkpoint = PhaseIncremental, historical.Checkpoint
	incremental, err := executor.FetchPage(context.Background(), user, run)
	if err != nil || incremental.AppliedCount != 1 || provider.changes != 1 || resolver.user != user || resolver.account != account {
		t.Fatalf("incremental = %+v resolver=%q/%q, %v", incremental, resolver.user, resolver.account, err)
	}
	if writer.catalogs != 2 {
		t.Fatalf("catalog writes = %d", writer.catalogs)
	}
}

func TestMicrosoftExecutorRequestsBoundedRecoveryForExpiredDelta(t *testing.T) {
	provider := &fakeMicrosoftProvider{deltaExpired: true}
	executor, _ := NewMicrosoftExecutor(&fakeMicrosoftResolver{provider: provider}, &fakeMicrosoftWriter{})
	history, _ := microsoftgraph.NewDeltaCursor([]string{"folder-a"})
	checkpoint, _ := json.Marshal(microsoftCheckpoint{History: history, CatalogDone: true, FolderIDs: []string{"folder-a"}})
	_, err := executor.FetchPage(context.Background(), "0199ed3b-c950-7000-8000-000000000001", Run{AccountID: "0199ed3b-c950-7000-8000-000000000016", Phase: PhaseIncremental, Checkpoint: checkpoint})
	if !errors.Is(err, ErrRemoteCursorInvalid) {
		t.Fatalf("expired delta = %v", err)
	}
}

func TestMicrosoftReconciliationRefreshesCatalogAndRecentWindow(t *testing.T) {
	provider := &fakeMicrosoftProvider{}
	executor, _ := NewMicrosoftExecutor(&fakeMicrosoftResolver{provider: provider}, &fakeMicrosoftWriter{})
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	executor.now = func() time.Time { return now }
	run := Run{AccountID: "0199ed3b-c950-7000-8000-000000000016", Phase: PhaseReconcile, Checkpoint: json.RawMessage(`{}`)}
	first, err := executor.FetchPage(context.Background(), "0199ed3b-c950-7000-8000-000000000001", run)
	if err != nil || !first.HasMore {
		t.Fatalf("reconcile catalog = %+v, %v", first, err)
	}
	run.Checkpoint = first.Checkpoint
	second, err := executor.FetchPage(context.Background(), "0199ed3b-c950-7000-8000-000000000001", run)
	if err != nil || !second.HasMore {
		t.Fatalf("reconcile catalog second = %+v, %v", second, err)
	}
	run.Checkpoint = second.Checkpoint
	result, err := executor.FetchPage(context.Background(), "0199ed3b-c950-7000-8000-000000000001", run)
	if err != nil || result.HasMore || provider.after == nil || !provider.after.Equal(now.Add(-RecentWindow)) || provider.before != nil {
		t.Fatalf("reconcile backfill = %+v, %v", result, err)
	}
}

var _ MicrosoftProvider = (*fakeMicrosoftProvider)(nil)
