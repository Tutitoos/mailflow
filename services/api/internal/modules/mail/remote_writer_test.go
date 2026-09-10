package mail

import (
	"context"
	"testing"
	"time"
)

func TestRemotePageWriterMaterializesIdempotentlyAndRecoversDeletion(t *testing.T) {
	_, pool, userID, accountID := mailboxRepositoryFixture(t)
	ctx := context.Background()
	writer := NewRemotePageWriter()
	stamp := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	catalog := CatalogPage{
		Mailboxes: []RemoteMailbox{{RemoteID: "INBOX", Name: "Inbox", Role: MailboxInbox, Selectable: true, TotalCount: 1, UnreadCount: 1}},
		Labels: []RemoteLabel{
			{RemoteID: "CATEGORY_PERSONAL", Name: "Primary", Kind: LabelCategory, Category: categoryPointer(CategoryPrimary), TotalCount: 1, UnreadCount: 1},
			{RemoteID: "Label_1", Name: "Fixture", Kind: LabelUser, TotalCount: 1},
		},
	}
	page := ChangePage{Messages: []RemoteMessage{{
		RemoteID: "remote-message", ThreadID: "remote-thread", SentAt: stamp, Category: CategoryPrimary,
		LabelIDs: []string{"INBOX", "CATEGORY_PERSONAL", "Label_1"},
		Content:  NormalizedMessageContent{Subject: "Sanitized fixture", BodyText: "Fixture body", Addresses: []MessageAddressInput{{Role: AddressFrom, Address: "sender@example.test"}}, Attachments: []AttachmentInput{{RemoteID: "attachment", Filename: "fixture.txt", MediaType: "text/plain", Disposition: "attachment", SizeBytes: 12}}},
	}}}
	for attempt := 0; attempt < 2; attempt++ {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.ApplyGmailPage(ctx, tx, userID, accountID, catalog, page); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for table, expected := range map[string]int{"mailboxes": 1, "labels": 2, "threads": 1, "messages": 1, "message_addresses": 1, "message_attachments": 1, "message_mailboxes": 1, "message_labels": 2} {
		var count int
		if err := pool.QueryRow(ctx, "select count(*) from "+table).Scan(&count); err != nil || count != expected {
			t.Fatalf("%s count = %d, %v", table, count, err)
		}
	}
	page.Messages[0].LabelIDs = nil
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.ApplyGmailPage(ctx, tx, userID, accountID, CatalogPage{}, page); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for table, expected := range map[string]int{"message_mailboxes": 0, "message_labels": 1} {
		var count int
		if err := pool.QueryRow(ctx, "select count(*) from "+table).Scan(&count); err != nil || count != expected {
			t.Fatalf("replaced %s count = %d, %v", table, count, err)
		}
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	older := ChangePage{Messages: []RemoteMessage{{RemoteID: "older-message", ThreadID: "remote-thread", SentAt: stamp.Add(-time.Hour), Category: CategoryPromotions, Content: NormalizedMessageContent{BodyText: "Older fixture"}}}}
	if err := writer.ApplyGmailPage(ctx, tx, userID, accountID, CatalogPage{}, older); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var category string
	if err := pool.QueryRow(ctx, `select category from threads where remote_id = 'remote-thread'`).Scan(&category); err != nil || category != string(CategoryPrimary) {
		t.Fatalf("thread category=%q error=%v", category, err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.ApplyGmailPage(ctx, tx, userID, accountID, CatalogPage{}, ChangePage{DeletedRemoteIDs: []string{"remote-message", "remote-message", "older-message"}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var messageDeleted, threadDeleted bool
	if err := pool.QueryRow(ctx, `select messages.deleted_at is not null, threads.deleted_at is not null from messages join threads on threads.id = messages.thread_id where messages.remote_id = 'remote-message'`).Scan(&messageDeleted, &threadDeleted); err != nil || !messageDeleted || !threadDeleted {
		t.Fatalf("deletion state message=%v thread=%v error=%v", messageDeleted, threadDeleted, err)
	}
}

func categoryPointer(value Category) *Category { return &value }
