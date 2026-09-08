package imap

import (
	"context"
	"errors"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFolderRepositoryPreservesIdentityAndInvalidatesOnlyChangedUIDValidity(t *testing.T) {
	repository, pool, userID, accountID := folderRepositoryFixture(t)
	ctx := context.Background()
	initial := normalizedFolderFixture(t,
		DiscoveredFolder{WireName: "Projects/Invoices", Name: "Projects/Invoices", Delimiter: "/", Selectable: true, Subscribed: true, UIDNext: int64Pointer(20), UIDValidity: int64Pointer(101)},
		DiscoveredFolder{WireName: "Receipts", Name: "Receipts", Delimiter: "/", Selectable: true, UIDNext: int64Pointer(30), UIDValidity: int64Pointer(202)},
	)
	first, err := repository.Reconcile(ctx, userID, accountID, initial)
	if err != nil || len(first.Folders) != 2 || first.ReconciliationRequired {
		t.Fatalf("initial reconciliation = %+v, error=%v", first, err)
	}
	firstIDs := map[string]string{}
	identities := map[string]string{}
	for _, folder := range first.Folders {
		firstIDs[folder.Name] = folder.MailboxID
		identities[folder.Name] = folder.RemoteID
	}
	if _, err := pool.Exec(ctx, "update imap_folder_cursors set next_uid = 15 where identity_key = $1", identities["Projects/Invoices"]); err != nil {
		t.Fatal(err)
	}

	delimiterVariation := normalizedFolderFixture(t,
		DiscoveredFolder{WireName: "Projects.Invoices", Name: "Projects.Invoices", Delimiter: ".", Selectable: true, UIDNext: int64Pointer(21), UIDValidity: int64Pointer(101)},
		DiscoveredFolder{WireName: "Receipts", Name: "Receipts", Delimiter: "/", Selectable: true, UIDNext: int64Pointer(31), UIDValidity: int64Pointer(202)},
	)
	second, err := repository.Reconcile(ctx, userID, accountID, delimiterVariation)
	if err != nil || len(second.Folders) != 2 {
		t.Fatalf("delimiter reconciliation = %+v, error=%v", second, err)
	}
	for _, folder := range second.Folders {
		if folder.Name == "Projects.Invoices" && (folder.MailboxID != firstIDs["Projects/Invoices"] || folder.NextUID == nil || *folder.NextUID != 15) {
			t.Fatalf("stable cursor = %+v", folder)
		}
	}

	renamed := normalizedFolderFixture(t,
		DiscoveredFolder{WireName: "Projects.Archived", Name: "Projects.Archived", Delimiter: ".", Selectable: true, UIDNext: int64Pointer(22), UIDValidity: int64Pointer(101)},
		DiscoveredFolder{WireName: "Receipts", Name: "Receipts", Delimiter: "/", Selectable: true, UIDNext: int64Pointer(32), UIDValidity: int64Pointer(303)},
	)
	third, err := repository.Reconcile(ctx, userID, accountID, renamed)
	if err != nil || len(third.Folders) != 2 || !third.ReconciliationRequired {
		t.Fatalf("rename and UIDVALIDITY reconciliation = %+v, error=%v", third, err)
	}
	states := map[string]FolderState{}
	for _, folder := range third.Folders {
		states[folder.Name] = folder
	}
	if folder := states["Projects.Archived"]; folder.MailboxID != firstIDs["Projects/Invoices"] || folder.NextUID == nil || *folder.NextUID != 15 || folder.CursorState != FolderCursorActive {
		t.Fatalf("renamed folder = %+v", folder)
	}
	if folder := states["Receipts"]; folder.CursorState != FolderCursorResyncRequired || folder.NextUID == nil || *folder.NextUID != 1 || folder.InvalidationReason == nil || *folder.InvalidationReason != "uid_validity_changed" {
		t.Fatalf("invalidated folder = %+v", folder)
	}
	var mailboxCount int
	if err := pool.QueryRow(ctx, "select count(*) from mailboxes where account_id = $1", accountID).Scan(&mailboxCount); err != nil || mailboxCount != 2 {
		t.Fatalf("mailbox count=%d error=%v", mailboxCount, err)
	}
}

func TestFolderRepositoryMarksMissingAndNonselectableFoldersWithoutCursors(t *testing.T) {
	repository, pool, userID, accountID := folderRepositoryFixture(t)
	ctx := context.Background()
	initial := normalizedFolderFixture(t,
		DiscoveredFolder{WireName: "INBOX", Name: "INBOX", Role: "inbox", Selectable: true, UIDNext: int64Pointer(2), UIDValidity: int64Pointer(10)},
		DiscoveredFolder{WireName: "Projects", Name: "Projects", Delimiter: "/", Selectable: false},
	)
	result, err := repository.Reconcile(ctx, userID, accountID, initial)
	if err != nil || len(result.Folders) != 2 {
		t.Fatalf("initial folders = %+v, error=%v", result, err)
	}
	for _, folder := range result.Folders {
		if folder.Name == "Projects" && (folder.CursorState != FolderCursorNotSelectable || folder.NextUID != nil) {
			t.Fatalf("nonselectable folder = %+v", folder)
		}
	}
	if _, err := repository.Reconcile(ctx, userID, accountID, initial[:1]); err != nil {
		t.Fatalf("mark missing: %v", err)
	}
	var state string
	var selectable bool
	if err := pool.QueryRow(ctx, `select cursors.state, mailboxes.selectable from imap_folder_cursors cursors join mailboxes on mailboxes.id = cursors.mailbox_id where cursors.identity_key = $1`, initial[1].IdentityKey).Scan(&state, &selectable); err != nil || state != string(FolderCursorMissing) || selectable {
		t.Fatalf("missing state=%q selectable=%v error=%v", state, selectable, err)
	}
	if _, err := repository.Reconcile(ctx, "0199ed3b-c950-7000-8000-000000000099", accountID, initial[:1]); !errors.Is(err, ErrFolderPersistence) {
		t.Fatalf("cross-owner reconciliation error=%v", err)
	}
}

func normalizedFolderFixture(t *testing.T, folders ...DiscoveredFolder) []DiscoveredFolder {
	t.Helper()
	if err := normalizeDiscoveredFolders(folders); err != nil {
		t.Fatal(err)
	}
	return folders
}

func folderRepositoryFixture(t *testing.T) (*FolderRepositoryStore, *pgxpool.Pool, string, string) {
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
	userID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000059")
	accountID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000159")
	if _, err := pool.Exec(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "imap-owner@example.test", "Owner"); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into accounts (id, user_id, provider, remote_id, display_name, encrypted_credentials, credential_nonce, capabilities) values ($1, $2, 'imap', 'imap-owner', 'Personal', $3, $4, '{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	return NewFolderRepository(pool), pool, userID.String(), accountID.String()
}
