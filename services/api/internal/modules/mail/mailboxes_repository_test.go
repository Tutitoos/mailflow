package mail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMailboxRepositoryReconcilesWithoutLosingLocalMetadata(t *testing.T) {
	repository, pool, userID, accountID := mailboxRepositoryFixture(t)
	ctx := context.Background()
	syncedAt := time.Date(2026, time.September, 7, 9, 30, 0, 0, time.UTC)

	created, err := repository.ReconcileMailbox(ctx, ReconcileMailboxInput{
		UserID: userID, AccountID: accountID, RemoteID: "INBOX", RemoteName: "Inbox",
		Role: MailboxInbox, Selectable: true, TotalCount: 42, UnreadCount: 5,
		RemoteRevision: "revision-1", LastSyncedAt: &syncedAt,
	})
	if err != nil {
		t.Fatalf("reconcile mailbox: %v", err)
	}
	if created.Role != MailboxInbox || created.DisplayName() != "Inbox" || created.TotalCount != 42 || created.UnreadCount != 5 {
		t.Fatalf("created mailbox = %+v", created)
	}
	if created.LastSyncedAt == nil || !created.LastSyncedAt.Equal(syncedAt) {
		t.Fatalf("last synced at = %v", created.LastSyncedAt)
	}

	renamed, err := repository.RenameMailbox(ctx, userID, accountID, created.ID, "Personal inbox")
	if err != nil || renamed.DisplayName() != "Personal inbox" {
		t.Fatalf("rename mailbox: mailbox=%+v error=%v", renamed, err)
	}
	reconciled, err := repository.ReconcileMailbox(ctx, ReconcileMailboxInput{
		UserID: userID, AccountID: accountID, RemoteID: "INBOX", RemoteName: "Provider Inbox",
		Role: MailboxInbox, Selectable: true, TotalCount: 50, UnreadCount: 7,
		RemoteRevision: "revision-2", LastSyncedAt: &syncedAt,
	})
	if err != nil {
		t.Fatalf("repeat reconciliation: %v", err)
	}
	if reconciled.ID != created.ID || reconciled.RemoteName != "Provider Inbox" || reconciled.DisplayName() != "Personal inbox" {
		t.Fatalf("reconciled mailbox = %+v", reconciled)
	}
	if reconciled.RemoteRevision == nil || *reconciled.RemoteRevision != "revision-2" {
		t.Fatalf("remote revision = %v", reconciled.RemoteRevision)
	}

	updated, err := repository.UpdateMailboxCounters(ctx, userID, accountID, created.ID, 55, 9)
	if err != nil || updated.TotalCount != 55 || updated.UnreadCount != 9 {
		t.Fatalf("update mailbox counters: mailbox=%+v error=%v", updated, err)
	}
	items, err := repository.ListMailboxes(ctx, userID, accountID)
	if err != nil || len(items) != 1 || items[0].ID != created.ID {
		t.Fatalf("list mailboxes: items=%+v error=%v", items, err)
	}

	otherUser := "0199ed3b-c950-7000-8000-000000000099"
	if _, err := repository.ReconcileMailbox(ctx, ReconcileMailboxInput{
		UserID: otherUser, AccountID: accountID, RemoteID: "SENT", RemoteName: "Sent",
		Role: MailboxSent, Selectable: true,
	}); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("cross-owner reconcile error = %v", err)
	}
	if _, err := repository.RenameMailbox(ctx, otherUser, accountID, created.ID, "Hidden"); !errors.Is(err, ErrMailboxNotFound) {
		t.Fatalf("cross-owner rename error = %v", err)
	}
	unauthorized, err := repository.ListMailboxes(ctx, otherUser, accountID)
	if err != nil || len(unauthorized) != 0 {
		t.Fatalf("cross-owner list: items=%+v error=%v", unauthorized, err)
	}
	if _, err := repository.UpdateMailboxCounters(ctx, userID, accountID, created.ID, 1, 2); !errors.Is(err, ErrInvalidMailbox) {
		t.Fatalf("invalid counters error = %v", err)
	}

	var stored int
	if err := pool.QueryRow(ctx, "select count(*) from mailboxes where account_id = $1 and remote_id = 'INBOX'", accountID).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored mailbox count=%d error=%v", stored, err)
	}
}

func TestLabelRepositoryRoundTripsProviderAndCategoryLabels(t *testing.T) {
	repository, _, userID, accountID := mailboxRepositoryFixture(t)
	ctx := context.Background()
	syncedAt := time.Date(2026, time.September, 7, 10, 0, 0, 0, time.UTC)

	providerLabel, err := repository.ReconcileProviderLabel(ctx, ReconcileProviderLabelInput{
		UserID: userID, AccountID: accountID, RemoteID: "Label_42", RemoteName: "Receipts",
		Kind: LabelUser, Color: "#111111", TotalCount: 12, UnreadCount: 2,
		RemoteRevision: "provider-1", LastSyncedAt: &syncedAt,
	})
	if err != nil {
		t.Fatalf("reconcile provider label: %v", err)
	}
	if providerLabel.RemoteID == nil || *providerLabel.RemoteID != "Label_42" || providerLabel.Kind != LabelUser {
		t.Fatalf("provider label = %+v", providerLabel)
	}
	renamed, err := repository.RenameLabel(ctx, userID, accountID, providerLabel.ID, "Invoices")
	if err != nil || renamed.DisplayName() != "Invoices" {
		t.Fatalf("rename provider label: label=%+v error=%v", renamed, err)
	}
	reconciled, err := repository.ReconcileProviderLabel(ctx, ReconcileProviderLabelInput{
		UserID: userID, AccountID: accountID, RemoteID: "Label_42", RemoteName: "Provider Receipts",
		Kind: LabelUser, TotalCount: 20, UnreadCount: 4, RemoteRevision: "provider-2",
	})
	if err != nil || reconciled.ID != providerLabel.ID || reconciled.DisplayName() != "Invoices" {
		t.Fatalf("repeat provider label reconciliation: label=%+v error=%v", reconciled, err)
	}

	category, err := repository.EnsureCategoryLabel(ctx, EnsureCategoryLabelInput{
		UserID: userID, AccountID: accountID, Category: CategoryPromotions,
		Name: "Promotions", Color: "#666666",
	})
	if err != nil {
		t.Fatalf("ensure category label: %v", err)
	}
	if category.RemoteID != nil || category.Category == nil || *category.Category != CategoryPromotions || category.Kind != LabelCategory {
		t.Fatalf("category label = %+v", category)
	}
	categoryAgain, err := repository.EnsureCategoryLabel(ctx, EnsureCategoryLabelInput{
		UserID: userID, AccountID: accountID, Category: CategoryPromotions,
		Name: "Offers", Color: "#777777",
	})
	if err != nil || categoryAgain.ID != category.ID || categoryAgain.RemoteName != "Offers" {
		t.Fatalf("repeat category reconciliation: label=%+v error=%v", categoryAgain, err)
	}

	updated, err := repository.UpdateLabelCounters(ctx, userID, accountID, providerLabel.ID, 25, 3)
	if err != nil || updated.TotalCount != 25 || updated.UnreadCount != 3 {
		t.Fatalf("update label counters: label=%+v error=%v", updated, err)
	}
	labels, err := repository.ListLabels(ctx, userID, accountID)
	if err != nil || len(labels) != 2 {
		t.Fatalf("list labels: labels=%+v error=%v", labels, err)
	}

	otherUser := "0199ed3b-c950-7000-8000-000000000099"
	if _, err := repository.RenameLabel(ctx, otherUser, accountID, providerLabel.ID, "Hidden"); !errors.Is(err, ErrLabelNotFound) {
		t.Fatalf("cross-owner label rename error = %v", err)
	}
	unauthorized, err := repository.ListLabels(ctx, otherUser, accountID)
	if err != nil || len(unauthorized) != 0 {
		t.Fatalf("cross-owner label list: labels=%+v error=%v", unauthorized, err)
	}
	if _, err := repository.EnsureCategoryLabel(ctx, EnsureCategoryLabelInput{UserID: userID, AccountID: accountID, Category: "unknown", Name: "Invalid"}); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("invalid category error = %v", err)
	}
}

func mailboxRepositoryFixture(t *testing.T) (*MailboxLabelRepositoryStore, *pgxpool.Pool, string, string) {
	t.Helper()
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	userID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000027")
	accountID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000127")
	if _, err := pool.Exec(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "mailbox-owner@example.test", "Owner"); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into accounts (
			id, user_id, provider, remote_id, display_name,
			encrypted_credentials, credential_nonce, capabilities
		) values ($1, $2, 'google', 'mailbox-owner', 'Personal', $3, $4, '{}')
	`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	return NewMailboxLabelRepository(dbgen.New(pool)), pool, userID.String(), accountID.String()
}
