package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestThreadRepositoryScopesIdempotentThreadsAndStablePages(t *testing.T) {
	_, pool, userID, accountID := mailboxRepositoryFixture(t)
	ctx := context.Background()
	repository := NewThreadRepository(pool)
	secondAccountID := createThreadTestAccount(t, pool, userID, "second-account")
	stamp := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)

	first, err := repository.UpsertThread(ctx, UpsertThreadInput{
		UserID: userID, AccountID: accountID, RemoteID: "shared-remote",
		LastMessageAt: stamp, IsRead: false, Category: CategoryPrimary,
	})
	if err != nil {
		t.Fatalf("upsert first thread: %v", err)
	}
	repeated, err := repository.UpsertThread(ctx, UpsertThreadInput{
		UserID: userID, AccountID: accountID, RemoteID: "shared-remote",
		LastMessageAt: stamp, IsRead: true, IsStarred: true, Category: CategoryPromotions,
	})
	if err != nil || repeated.ID != first.ID || !repeated.IsRead || !repeated.IsStarred || repeated.Category != CategoryPromotions {
		t.Fatalf("repeat thread upsert: thread=%+v error=%v", repeated, err)
	}
	otherAccount, err := repository.UpsertThread(ctx, UpsertThreadInput{
		UserID: userID, AccountID: secondAccountID, RemoteID: "shared-remote",
		LastMessageAt: stamp, Category: CategoryPrimary,
	})
	if err != nil || otherAccount.ID == first.ID {
		t.Fatalf("account-scoped thread: thread=%+v error=%v", otherAccount, err)
	}

	for index := 0; index < 4; index++ {
		if _, err := repository.UpsertThread(ctx, UpsertThreadInput{
			UserID: userID, AccountID: accountID, RemoteID: fmt.Sprintf("thread-%d", index),
			LastMessageAt: stamp, Category: CategoryPrimary,
		}); err != nil {
			t.Fatalf("create paginated thread %d: %v", index, err)
		}
	}
	seen := make(map[string]struct{})
	var cursor *ThreadCursor
	for {
		page, err := repository.ListThreads(ctx, userID, accountID, cursor, 2)
		if err != nil {
			t.Fatalf("list thread page: %v", err)
		}
		for _, thread := range page.Items {
			if _, duplicate := seen[thread.ID]; duplicate {
				t.Fatalf("thread %s appeared in multiple pages", thread.ID)
			}
			seen[thread.ID] = struct{}{}
		}
		if page.Next == nil {
			break
		}
		cursor = page.Next
	}
	if len(seen) != 5 {
		t.Fatalf("paginated thread count = %d, want 5", len(seen))
	}

	otherUser := "0199ed3b-c950-7000-8000-000000000099"
	if _, err := repository.GetThread(ctx, otherUser, accountID, first.ID); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("cross-owner get thread error = %v", err)
	}
	unauthorized, err := repository.ListThreads(ctx, otherUser, accountID, nil, 20)
	if err != nil || len(unauthorized.Items) != 0 {
		t.Fatalf("cross-owner thread list: page=%+v error=%v", unauthorized, err)
	}
	if _, err := repository.UpsertThread(ctx, UpsertThreadInput{
		UserID: otherUser, AccountID: accountID, RemoteID: "hidden",
		LastMessageAt: stamp, Category: CategoryPrimary,
	}); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("cross-owner upsert thread error = %v", err)
	}
}

func TestMessageRepositoryRethreadsAddressesAndStateAtomically(t *testing.T) {
	_, pool, userID, accountID := mailboxRepositoryFixture(t)
	ctx := context.Background()
	repository := NewThreadRepository(pool)
	stamp := time.Date(2026, time.September, 7, 13, 0, 0, 0, time.UTC)
	firstThread := mustUpsertTestThread(t, repository, userID, accountID, "thread-a", stamp)
	secondThread := mustUpsertTestThread(t, repository, userID, accountID, "thread-b", stamp)

	message, err := repository.UpsertMessage(ctx, UpsertMessageInput{
		UserID: userID, AccountID: accountID, ThreadID: firstThread.ID,
		RemoteID: "message-1", MessageID: "<message-1@example.test>",
		References: []string{"<parent@example.test>"}, SentAt: stamp,
		InReplyTo: []string{"parent@example.test"}, Subject: "Stored subject",
		BodyText: "Safe text", BodyHTML: `<p onclick="private()">Safe text</p><script>private()</script>`,
		Addresses: []MessageAddressInput{
			{Role: AddressFrom, DisplayName: "Sender", Address: "sender@example.test"},
			{Role: AddressTo, DisplayName: "Owner", Address: "owner@example.test"},
		},
		Attachments: []AttachmentInput{{RemoteID: "remote-file", Filename: "report.pdf", MediaType: "application/pdf", Disposition: "attachment", SizeBytes: 42}},
	})
	if err != nil {
		t.Fatalf("upsert message: %v", err)
	}
	if len(message.Addresses) != 2 || message.Addresses[0].Position != 0 || message.Addresses[1].Position != 0 || len(message.Attachments) != 1 || message.Subject != "Stored subject" || message.BodyHTML != "<p>Safe text</p>" {
		t.Fatalf("message content = %+v", message)
	}
	attachmentID := message.Attachments[0].ID
	repeated, err := repository.UpsertMessage(ctx, UpsertMessageInput{
		UserID: userID, AccountID: accountID, ThreadID: firstThread.ID,
		RemoteID: "message-1", MessageID: "<message-1@example.test>", SentAt: stamp,
		Attachments: []AttachmentInput{{Filename: "renamed.pdf", MediaType: "application/pdf", Disposition: "attachment", SizeBytes: 43}},
	})
	if err != nil || len(repeated.Attachments) != 1 || repeated.Attachments[0].ID != attachmentID || repeated.Attachments[0].Filename == nil || *repeated.Attachments[0].Filename != "renamed.pdf" {
		t.Fatalf("idempotent attachment update = %+v, %v", repeated.Attachments, err)
	}
	firstSummary, err := repository.GetThread(ctx, userID, accountID, firstThread.ID)
	if err != nil || firstSummary.MessageCount != 1 || firstSummary.UnreadCount != 1 {
		t.Fatalf("first thread summary: thread=%+v error=%v", firstSummary, err)
	}

	rethreaded, err := repository.UpsertMessage(ctx, UpsertMessageInput{
		UserID: userID, AccountID: accountID, ThreadID: secondThread.ID,
		RemoteID: "message-1", MessageID: "<message-1@example.test>", SentAt: stamp,
		IsRead: true, IsStarred: true, IsImportant: true,
		Addresses: []MessageAddressInput{{Role: AddressReplyTo, Address: "reply@example.test"}},
	})
	if err != nil || rethreaded.ID != message.ID || rethreaded.ThreadID != secondThread.ID || len(rethreaded.Addresses) != 1 {
		t.Fatalf("rethread message: message=%+v error=%v", rethreaded, err)
	}
	firstSummary, err = repository.GetThread(ctx, userID, accountID, firstThread.ID)
	if err != nil || firstSummary.MessageCount != 0 || firstSummary.UnreadCount != 0 || firstSummary.IsStarred {
		t.Fatalf("empty previous thread summary: thread=%+v error=%v", firstSummary, err)
	}
	secondSummary, err := repository.GetThread(ctx, userID, accountID, secondThread.ID)
	if err != nil || secondSummary.MessageCount != 1 || !secondSummary.IsRead || !secondSummary.IsStarred || !secondSummary.IsImportant {
		t.Fatalf("new thread summary: thread=%+v error=%v", secondSummary, err)
	}

	secondMessage, err := repository.UpsertMessage(ctx, UpsertMessageInput{
		UserID: userID, AccountID: accountID, ThreadID: secondThread.ID,
		RemoteID: "message-2", SentAt: stamp, IsRead: true, IsStarred: true, IsImportant: true,
	})
	if err != nil {
		t.Fatalf("upsert second message: %v", err)
	}
	firstPage, err := repository.ListMessages(ctx, userID, accountID, secondThread.ID, nil, 1)
	if err != nil || len(firstPage.Items) != 1 || firstPage.Next == nil {
		t.Fatalf("first message page: page=%+v error=%v", firstPage, err)
	}
	secondPage, err := repository.ListMessages(ctx, userID, accountID, secondThread.ID, firstPage.Next, 1)
	if err != nil || len(secondPage.Items) != 1 || secondPage.Next != nil || secondPage.Items[0].ID == firstPage.Items[0].ID {
		t.Fatalf("second message page: page=%+v error=%v", secondPage, err)
	}

	trueValue, falseValue := true, false
	deleted, err := repository.ApplyThreadState(ctx, userID, accountID, secondThread.ID, StatePatch{
		Read: &trueValue, Starred: &trueValue, Important: &trueValue, Deleted: &trueValue,
	})
	if err != nil || !deleted.IsRead || !deleted.IsStarred || !deleted.IsImportant || deleted.DeletedAt == nil || deleted.UnreadCount != 0 {
		t.Fatalf("apply thread state: thread=%+v error=%v", deleted, err)
	}
	if _, err := repository.ApplyMessageState(ctx, userID, accountID, secondThread.ID, secondMessage.ID, StatePatch{
		Read: &falseValue, Starred: &falseValue, Important: &falseValue, Deleted: &falseValue,
	}); err != nil {
		t.Fatalf("apply message state: %v", err)
	}
	aggregated, err := repository.GetThread(ctx, userID, accountID, secondThread.ID)
	if err != nil || aggregated.IsRead || !aggregated.IsStarred || !aggregated.IsImportant || aggregated.DeletedAt != nil || aggregated.UnreadCount != 1 {
		t.Fatalf("aggregated thread state: thread=%+v error=%v", aggregated, err)
	}

	otherUser := "0199ed3b-c950-7000-8000-000000000099"
	if _, err := repository.ApplyMessageState(ctx, otherUser, accountID, secondThread.ID, secondMessage.ID, StatePatch{Read: &trueValue}); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("cross-owner message update error = %v", err)
	}
	if _, err := repository.ApplyThreadState(ctx, userID, accountID, secondThread.ID, StatePatch{}); !errors.Is(err, ErrInvalidThread) {
		t.Fatalf("empty thread state patch error = %v", err)
	}
}

func TestMessageUpsertIsConcurrentAndIdempotent(t *testing.T) {
	_, pool, userID, accountID := mailboxRepositoryFixture(t)
	repository := NewThreadRepository(pool)
	stamp := time.Date(2026, time.September, 7, 14, 0, 0, 0, time.UTC)
	thread := mustUpsertTestThread(t, repository, userID, accountID, "concurrent-thread", stamp)

	const writers = 8
	ids := make(chan string, writers)
	errorsChannel := make(chan error, writers)
	var wait sync.WaitGroup
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			message, err := repository.UpsertMessage(context.Background(), UpsertMessageInput{
				UserID: userID, AccountID: accountID, ThreadID: thread.ID,
				RemoteID: "concurrent-message", SentAt: stamp,
				Addresses: []MessageAddressInput{{Role: AddressFrom, Address: "sender@example.test"}},
			})
			if err != nil {
				errorsChannel <- err
				return
			}
			ids <- message.ID
		}()
	}
	wait.Wait()
	close(ids)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("concurrent upsert: %v", err)
	}
	var expectedID string
	for id := range ids {
		if expectedID == "" {
			expectedID = id
		}
		if id != expectedID {
			t.Errorf("concurrent IDs differ: %s and %s", expectedID, id)
		}
	}
	var stored int
	if err := pool.QueryRow(context.Background(), "select count(*) from messages where account_id = $1 and remote_id = 'concurrent-message'", accountID).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored concurrent message count=%d error=%v", stored, err)
	}

	secondThread := mustUpsertTestThread(t, repository, userID, accountID, "concurrent-target", stamp)
	start := make(chan struct{})
	errorsChannel = make(chan error, 2)
	wait = sync.WaitGroup{}
	for _, target := range []string{thread.ID, secondThread.ID} {
		target := target
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := repository.UpsertMessage(context.Background(), UpsertMessageInput{
				UserID: userID, AccountID: accountID, ThreadID: target,
				RemoteID: "concurrent-message", SentAt: stamp,
			})
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent rethread: %v", err)
		}
	}
	var currentThread string
	if err := pool.QueryRow(context.Background(), "select thread_id::text from messages where account_id = $1 and remote_id = 'concurrent-message'", accountID).Scan(&currentThread); err != nil {
		t.Fatalf("load concurrent message thread: %v", err)
	}
	for _, candidate := range []Thread{thread, secondThread} {
		summary, err := repository.GetThread(context.Background(), userID, accountID, candidate.ID)
		if err != nil {
			t.Fatalf("load concurrent thread summary: %v", err)
		}
		wantCount := int32(0)
		if candidate.ID == currentThread {
			wantCount = 1
		}
		if summary.MessageCount != wantCount {
			t.Fatalf("thread %s count = %d, want %d for current %s", candidate.ID, summary.MessageCount, wantCount, currentThread)
		}
	}
}

func TestMessagePaginationUsesBoundedIndexWithOneHundredThousandRows(t *testing.T) {
	_, pool, userID, accountID := mailboxRepositoryFixture(t)
	ctx := context.Background()
	repository := NewThreadRepository(pool)
	stamp := time.Date(2026, time.September, 7, 15, 0, 0, 0, time.UTC)
	thread := mustUpsertTestThread(t, repository, userID, accountID, "large-thread", stamp)

	if _, err := pool.Exec(ctx, `
		insert into messages (
			id, thread_id, account_id, remote_id, sender, recipients, sent_at
		)
		select
			('00000000-0000-7000-8000-' || lpad(to_hex(item), 12, '0'))::uuid,
			$1, $2, 'bulk-' || item, '{}'::jsonb, '[]'::jsonb,
			$3::timestamptz + item * interval '1 microsecond'
		from generate_series(1, 100000) as item
	`, thread.ID, accountID, stamp); err != nil {
		t.Fatalf("insert 100,000 messages: %v", err)
	}
	if _, err := pool.Exec(ctx, "analyze messages"); err != nil {
		t.Fatalf("analyze messages: %v", err)
	}

	first, err := repository.ListMessages(ctx, userID, accountID, thread.ID, nil, 50)
	if err != nil || len(first.Items) != 50 || first.Next == nil {
		t.Fatalf("first large page: count=%d next=%v error=%v", len(first.Items), first.Next, err)
	}
	second, err := repository.ListMessages(ctx, userID, accountID, thread.ID, first.Next, 50)
	if err != nil || len(second.Items) != 50 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second large page: count=%d error=%v", len(second.Items), err)
	}

	rows, err := pool.Query(ctx, `
		explain (format text)
		select messages.*
		from messages
		join threads on threads.id = messages.thread_id and threads.account_id = messages.account_id
		join accounts on accounts.id = messages.account_id
		where messages.thread_id = $1
		  and messages.account_id = $2
		  and accounts.user_id = $3
		  and messages.sent_at > $4
		order by messages.sent_at, messages.id
		limit 51
	`, thread.ID, accountID, userID, stamp.Add(50_000*time.Microsecond))
	if err != nil {
		t.Fatalf("explain message page: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan explain plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read explain plan: %v", err)
	}
	if !strings.Contains(plan.String(), "messages_thread_sent_idx") {
		t.Fatalf("message page did not use bounded cursor index:\n%s", plan.String())
	}
}

func mustUpsertTestThread(t *testing.T, repository *ThreadRepositoryStore, userID, accountID, remoteID string, stamp time.Time) Thread {
	t.Helper()
	thread, err := repository.UpsertThread(context.Background(), UpsertThreadInput{
		UserID: userID, AccountID: accountID, RemoteID: remoteID,
		LastMessageAt: stamp, Category: CategoryPrimary,
	})
	if err != nil {
		t.Fatalf("upsert test thread %s: %v", remoteID, err)
	}
	return thread
}

func createThreadTestAccount(t *testing.T, pool *pgxpool.Pool, userID, remoteID string) string {
	t.Helper()
	accountID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("create account ID: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		insert into accounts (
			id, user_id, provider, remote_id, display_name,
			encrypted_credentials, credential_nonce, capabilities
		) values ($1, $2, 'google', $3, 'Secondary', $4, $5, '{}')
	`, accountID, userID, remoteID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("create secondary account: %v", err)
	}
	return accountID.String()
}
