package imap

import (
	"context"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/google/uuid"
)

func TestMessageLocationsReconcileChangedUIDAndEnforceOwnerScope(t *testing.T) {
	folders, pool, userID, accountID := folderRepositoryFixture(t)
	ctx := context.Background()
	discovered := normalizedFolderFixture(t,
		DiscoveredFolder{WireName: "INBOX", Name: "Inbox", Role: mail.MailboxInbox, Selectable: true, UIDNext: int64Pointer(10), UIDValidity: int64Pointer(7)},
		DiscoveredFolder{WireName: "Archive", Name: "Archive", Role: mail.MailboxArchive, Selectable: true, UIDNext: int64Pointer(20), UIDValidity: int64Pointer(8)},
	)
	if _, err := folders.Reconcile(ctx, userID, accountID, discovered); err != nil {
		t.Fatal(err)
	}
	inboxRemoteID, archiveRemoteID := "", ""
	for _, folder := range discovered {
		switch folder.Role {
		case mail.MailboxInbox:
			inboxRemoteID = folder.IdentityKey
		case mail.MailboxArchive:
			archiveRemoteID = folder.IdentityKey
		}
	}
	if inboxRemoteID == "" || archiveRemoteID == "" {
		t.Fatal("mailbox fixtures were not normalized")
	}
	writer := mail.NewRemotePageWriter()
	apply := func(uid int64) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		page := mail.ChangePage{Messages: []mail.RemoteMessage{{
			RemoteID: "stable-message", ThreadID: "stable-thread", SentAt: time.Now().UTC(), Category: mail.CategoryPrimary,
			Locations: []mail.RemoteLocation{{MailboxID: inboxRemoteID, UIDValidity: 7, UID: uid}},
			Content:   mail.NormalizedMessageContent{MessageID: "stable@example.test", BodyText: "fixture"},
		}}}
		if err := writer.ApplyRemotePage(ctx, tx, userID, accountID, mail.CatalogPage{}, page); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	apply(3)
	apply(9)
	store, err := NewDatabaseProviderStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	locations, err := store.Locations(ctx, userID, accountID, []string{"stable-message"})
	if err != nil || len(locations) != 1 || locations[0].UID != 9 || locations[0].WireName != "INBOX" {
		t.Fatalf("reconciled locations=%+v error=%v", locations, err)
	}
	otherUser := uuid.MustParse("0199ed3b-c950-7000-8000-000000000099").String()
	unauthorized, err := store.Locations(ctx, otherUser, accountID, []string{"stable-message"})
	if err != nil || len(unauthorized) != 0 {
		t.Fatalf("cross-owner locations=%+v error=%v", unauthorized, err)
	}
	destination := MessageLocation{MessageRemoteID: "stable-message", MailboxRemoteID: archiveRemoteID, WireName: "Archive", UIDValidity: 8, UID: 21}
	if err := store.MoveLocation(ctx, userID, accountID, locations[0], destination); err != nil {
		t.Fatal(err)
	}
	moved, err := store.Locations(ctx, userID, accountID, []string{"stable-message"})
	if err != nil || len(moved) != 1 || moved[0].MailboxRemoteID != archiveRemoteID || moved[0].UID != 21 {
		t.Fatalf("moved locations=%+v error=%v", moved, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page := mail.ChangePage{LocationSnapshots: []mail.RemoteLocationSnapshot{{MailboxID: archiveRemoteID, UIDValidity: 8, PresentUIDs: []int64{}}}}
	if err := writer.ApplyRemotePage(ctx, tx, userID, accountID, mail.CatalogPage{}, page); err != nil {
		tx.Rollback(ctx) //nolint:errcheck
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	locations, err = store.Locations(ctx, userID, accountID, []string{"stable-message"})
	var deleted bool
	if scanErr := pool.QueryRow(ctx, "select deleted_at is not null from messages where remote_id = 'stable-message'").Scan(&deleted); scanErr != nil {
		t.Fatal(scanErr)
	}
	if err != nil || len(locations) != 0 || !deleted {
		t.Fatalf("orphan cleanup locations=%+v deleted=%v error=%v", locations, deleted, err)
	}
}
