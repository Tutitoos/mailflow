package mail

import (
	"context"
	"testing"
	"time"
)

func TestInboxProjectionIsCategoryScopedAndCursorStable(t *testing.T) {
	_, pool, userID, accountID := mailboxRepositoryFixture(t)
	repository := NewThreadRepository(pool)
	ctx := context.Background()
	stamp := time.Date(2026, 9, 7, 17, 0, 0, 0, time.UTC)

	for index, category := range []Category{CategoryPrimary, CategoryPrimary, CategorySocial} {
		thread, err := repository.UpsertThread(ctx, UpsertThreadInput{UserID: userID, AccountID: accountID, RemoteID: "inbox-thread-" + string(rune('a'+index)), LastMessageAt: stamp.Add(time.Duration(index) * time.Minute), IsRead: index > 0, Category: category})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repository.UpsertMessage(ctx, UpsertMessageInput{
			UserID: userID, AccountID: accountID, ThreadID: thread.ID, RemoteID: "inbox-message-" + string(rune('a'+index)),
			Subject: "", BodyText: "", SentAt: stamp.Add(time.Duration(index) * time.Minute), IsRead: index > 0,
			Addresses:   []MessageAddressInput{{Role: AddressFrom, DisplayName: "Fixture sender", Address: "sender@example.test"}},
			Attachments: []AttachmentInput{{RemoteID: "attachment-" + string(rune('a'+index)), Filename: "fixture.txt", MediaType: "text/plain", Disposition: "attachment", SizeBytes: 7}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := repository.ListInbox(ctx, userID, accountID, CategoryPrimary, nil, 1)
	if err != nil || len(first.Items) != 1 || first.Next == nil {
		t.Fatalf("first inbox page: items=%d next=%v error=%v", len(first.Items), first.Next, err)
	}
	if first.Items[0].SenderName != "Fixture sender" || first.Items[0].AttachmentCount != 1 || first.Items[0].Category != CategoryPrimary {
		t.Fatalf("unexpected inbox projection: %+v", first.Items[0])
	}
	second, err := repository.ListInbox(ctx, userID, accountID, CategoryPrimary, first.Next, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second inbox page: items=%d error=%v", len(second.Items), err)
	}
	unauthorized, err := repository.ListInbox(ctx, "0199ed3b-c950-7000-8000-000000000099", accountID, CategoryPrimary, nil, 10)
	if err != nil || len(unauthorized.Items) != 0 {
		t.Fatalf("cross-owner inbox leaked data: items=%d error=%v", len(unauthorized.Items), err)
	}
}
