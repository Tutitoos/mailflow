package mail

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSearchRepositoryScopesFiltersAndPaginates(t *testing.T) {
	mailboxRepository, pool, userID, accountID := mailboxRepositoryFixture(t)
	ctx := context.Background()
	threads := NewThreadRepository(pool)
	stamp := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	thread := mustUpsertTestThread(t, threads, userID, accountID, "search-thread", stamp)
	mailbox, err := mailboxRepository.ReconcileMailbox(ctx, ReconcileMailboxInput{
		UserID: userID, AccountID: accountID, RemoteID: "INBOX", RemoteName: "Inbox", Role: MailboxInbox, Selectable: true,
	})
	if err != nil {
		t.Fatalf("create search mailbox: %v", err)
	}
	label, err := mailboxRepository.ReconcileProviderLabel(ctx, ReconcileProviderLabelInput{
		UserID: userID, AccountID: accountID, RemoteID: "Label_work", RemoteName: "Work", Kind: LabelUser,
	})
	if err != nil {
		t.Fatalf("create search label: %v", err)
	}
	target, err := threads.UpsertMessage(ctx, UpsertMessageInput{
		UserID: userID, AccountID: accountID, ThreadID: thread.ID, RemoteID: "search-target",
		Subject: "Project Atlas", BodyText: "quarterly needle report", SentAt: stamp, IsStarred: true,
		Addresses:   []MessageAddressInput{{Role: AddressFrom, Address: "sender@example.test"}, {Role: AddressTo, Address: "owner@example.test"}},
		Attachments: []AttachmentInput{{Filename: "report.pdf", MediaType: "application/pdf", Disposition: "attachment", SizeBytes: 42}},
	})
	if err != nil {
		t.Fatalf("create search target: %v", err)
	}
	if _, err := threads.UpsertMessage(ctx, UpsertMessageInput{
		UserID: userID, AccountID: accountID, ThreadID: thread.ID, RemoteID: "search-decoy",
		Subject: "Unrelated", BodyText: "quarterly needle report", SentAt: stamp.Add(-time.Hour), IsRead: true,
		Addresses: []MessageAddressInput{{Role: AddressFrom, Address: "other@example.test"}},
	}); err != nil {
		t.Fatalf("create search decoy: %v", err)
	}
	if _, err := pool.Exec(ctx, "insert into message_mailboxes (message_id, mailbox_id, account_id) values ($1, $2, $3)", target.ID, mailbox.ID, accountID); err != nil {
		t.Fatalf("link search mailbox: %v", err)
	}
	if _, err := pool.Exec(ctx, "insert into message_labels (message_id, label_id, account_id) values ($1, $2, $3)", target.ID, label.ID, accountID); err != nil {
		t.Fatalf("link search label: %v", err)
	}

	query, err := ParseSearch(`quarterly needle from:sender@example.test to:owner@example.test subject:Atlas after:2026-01-01 before:2026-02-01 has:attachment is:unread is:starred label:work in:inbox`)
	if err != nil {
		t.Fatalf("parse search query: %v", err)
	}
	page, err := threads.SearchMessages(ctx, userID, accountID, query, nil, 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].Message.ID != target.ID || len(page.Items[0].Message.Addresses) != 2 || len(page.Items[0].Message.Attachments) != 1 {
		t.Fatalf("filtered search = %+v, %v", page, err)
	}
	unauthorized, err := threads.SearchMessages(ctx, "0199ed3b-c950-7000-8000-000000000099", accountID, query, nil, 20)
	if err != nil || len(unauthorized.Items) != 0 {
		t.Fatalf("cross-owner search = %+v, %v", unauthorized, err)
	}

	for index := 0; index < 3; index++ {
		if _, err := threads.UpsertMessage(ctx, UpsertMessageInput{
			UserID: userID, AccountID: accountID, ThreadID: thread.ID,
			RemoteID: fmt.Sprintf("stable-%d", index), Subject: "stablecursor", SentAt: stamp,
		}); err != nil {
			t.Fatalf("create stable search message: %v", err)
		}
	}
	stable, _ := ParseSearch("stablecursor")
	seen := make(map[string]struct{})
	var cursor *SearchCursor
	for {
		page, err := threads.SearchMessages(ctx, userID, accountID, stable, cursor, 1)
		if err != nil {
			t.Fatalf("stable search page: %v", err)
		}
		for _, hit := range page.Items {
			if _, duplicate := seen[hit.Message.ID]; duplicate {
				t.Fatalf("duplicate search hit %s", hit.Message.ID)
			}
			seen[hit.Message.ID] = struct{}{}
		}
		if page.Next == nil {
			break
		}
		cursor = page.Next
	}
	if len(seen) != 3 {
		t.Fatalf("stable search count = %d", len(seen))
	}
}

func TestSearchRepositoryUsesIndexesUnderOneSecondAtOneHundredThousandMessages(t *testing.T) {
	_, pool, userID, accountID := mailboxRepositoryFixture(t)
	ctx := context.Background()
	repository := NewThreadRepository(pool)
	stamp := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	thread := mustUpsertTestThread(t, repository, userID, accountID, "search-volume", stamp)
	if _, err := pool.Exec(ctx, `
		insert into messages (id, thread_id, account_id, remote_id, sender, recipients, subject, body_text, sent_at)
		select
		  ('10000000-0000-7000-8000-' || lpad(to_hex(item), 12, '0'))::uuid,
		  $1, $2, 'search-bulk-' || item, '{}', '[]',
		  case when item = 77777 then 'needlemarker result' else 'ordinary message ' || item end,
		  'bounded synthetic content', $3::timestamptz + item * interval '1 microsecond'
		from generate_series(1, 100000) as item
	`, thread.ID, accountID, stamp); err != nil {
		t.Fatalf("insert search volume: %v", err)
	}
	if _, err := pool.Exec(ctx, "analyze messages"); err != nil {
		t.Fatalf("analyze search volume: %v", err)
	}
	query, _ := ParseSearch("needlemarker")
	started := time.Now()
	page, err := repository.SearchMessages(ctx, userID, accountID, query, nil, 20)
	elapsed := time.Since(started)
	if err != nil || len(page.Items) != 1 || page.Items[0].Message.Subject != "needlemarker result" {
		t.Fatalf("volume search = %+v, %v", page, err)
	}
	if elapsed >= time.Second {
		t.Fatalf("volume search took %s, target is below one second", elapsed)
	}
	typoQuery, _ := ParseSearch("needlemarkr")
	typoPage, err := repository.SearchMessages(ctx, userID, accountID, typoQuery, nil, 20)
	if err != nil || len(typoPage.Items) != 1 || typoPage.Items[0].Message.Subject != "needlemarker result" {
		t.Fatalf("trigram search = %+v, %v", typoPage, err)
	}
	rows, err := pool.Query(ctx, `explain (format text) select id from messages where search_vector @@ websearch_to_tsquery('simple', 'needlemarker')`)
	if err != nil {
		t.Fatalf("explain FTS: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan FTS plan: %v", err)
		}
		plan.WriteString(line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read FTS plan: %v", err)
	}
	if !strings.Contains(plan.String(), "messages_search_idx") {
		t.Fatalf("FTS plan did not use messages_search_idx: %s", plan.String())
	}
}
