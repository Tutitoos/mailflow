package mail

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestOptimisticThreadActionsAreAtomicAndScoped(t *testing.T) {
	mailboxes, pool, userID, accountID := mailboxRepositoryFixture(t)
	threads := NewThreadRepository(pool)
	actions := NewPendingActionRepository(pool)
	stamp := time.Date(2026, 9, 7, 17, 0, 0, 0, time.UTC)
	thread, err := threads.UpsertThread(context.Background(), UpsertThreadInput{UserID: userID, AccountID: accountID, RemoteID: "action-thread", LastMessageAt: stamp, Category: CategoryPrimary})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := threads.UpsertMessage(context.Background(), UpsertMessageInput{UserID: userID, AccountID: accountID, ThreadID: thread.ID, RemoteID: "action-message", SentAt: stamp, BodyText: "fixture"}); err != nil {
		t.Fatal(err)
	}
	label, err := mailboxes.ReconcileProviderLabel(context.Background(), ReconcileProviderLabelInput{UserID: userID, AccountID: accountID, RemoteID: "Label_1", RemoteName: "Fixture", Kind: LabelUser})
	if err != nil {
		t.Fatal(err)
	}

	for index, values := range []struct {
		kind          ActionKind
		desired       json.RawMessage
		authoritative json.RawMessage
	}{
		{ActionMarkRead, json.RawMessage(`{"read":true}`), json.RawMessage(`{"read":false}`)},
		{ActionAddLabel, json.RawMessage(`{"labelId":"` + label.ID + `","labelled":true}`), json.RawMessage(`{"labelId":"` + label.ID + `","labelled":false}`)},
		{ActionArchive, json.RawMessage(`{"archived":true}`), json.RawMessage(`{"archived":false}`)},
	} {
		input := EnqueueActionInput{UserID: userID, AccountID: accountID, IdempotencyKey: "optimistic-action-000" + string(rune('1'+index)), Kind: values.kind, TargetKind: ActionTargetThread, TargetID: thread.ID, DesiredState: values.desired, AuthoritativeState: values.authoritative, MaxAttempts: 3}
		if _, created, err := actions.Enqueue(context.Background(), input, func(ctx context.Context, tx pgx.Tx) error {
			return threads.ApplyActionState(ctx, tx, input, values.desired)
		}); err != nil || !created {
			t.Fatalf("enqueue %s: created=%v error=%v", values.kind, created, err)
		}
	}

	updated, err := threads.GetThread(context.Background(), userID, accountID, thread.ID)
	if err != nil || !updated.IsRead {
		t.Fatalf("optimistic read state = %+v error=%v", updated, err)
	}
	page, err := threads.ListInbox(context.Background(), userID, accountID, CategoryPrimary, nil, 10)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("archived inbox = %+v error=%v", page, err)
	}
	var labels int
	if err := pool.QueryRow(context.Background(), `select count(*) from message_labels where label_id = $1`, label.ID).Scan(&labels); err != nil || labels != 1 {
		t.Fatalf("optimistic labels = %d error=%v", labels, err)
	}
	labelled, err := threads.ThreadHasLabel(context.Background(), userID, accountID, thread.ID, label.ID)
	if err != nil || !labelled {
		t.Fatalf("thread label state = %v error=%v", labelled, err)
	}
}
