package mail

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/jackc/pgx/v5/pgxpool"
)

type outgoingTestProvider struct {
	drafts  int
	sends   int
	sendErr error
	last    string
	thread  string
}

func (provider *outgoingTestProvider) SaveDraft(_ context.Context, message OutgoingMessage) (string, error) {
	provider.drafts++
	provider.thread = message.ThreadID
	payload, _ := io.ReadAll(message.Raw)
	provider.last = string(payload)
	return "remote-draft", nil
}

func (provider *outgoingTestProvider) Send(_ context.Context, message OutgoingMessage) (string, error) {
	provider.sends++
	provider.thread = message.ThreadID
	payload, _ := io.ReadAll(message.Raw)
	provider.last = string(payload)
	return "remote-message", provider.sendErr
}

type outgoingTestResolver struct{ provider *outgoingTestProvider }

func (resolver outgoingTestResolver) ResolveOutgoingProvider(context.Context, string, string) (OutgoingProvider, error) {
	return resolver.provider, nil
}

func TestDeliveryCheckpointsRepliesAndNeverRepeatsSend(t *testing.T) {
	repository, pool, userID, accountID := draftFixture(t)
	message := outgoingSourceFixture(t, pool, userID, accountID)
	draft, err := repository.CreateDraft(context.Background(), CreateDraftInput{
		UserID: userID, AccountID: accountID, Now: time.Now().UTC(),
		Content: DraftContentInput{
			Subject: "Re: Fixture", BodyText: "Reply", BodyHTML: "<p>Reply</p>", Mode: ComposeReply, SourceMessageID: message.ID,
			Recipients: []MessageAddressInput{{Role: AddressTo, Address: "recipient@example.test"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &outgoingTestProvider{}
	service, err := NewDeliveryService(pool, repository, outgoingTestResolver{provider}, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := service.CheckpointDraft(context.Background(), userID, accountID, draft.ID)
	if err != nil || checkpoint.SyncStatus != DraftSynced || provider.drafts != 1 || provider.thread != "remote-thread" || !strings.Contains(provider.last, "In-Reply-To: <source@example.test>") {
		t.Fatalf("checkpoint=%+v drafts=%d thread=%q payload=%q error=%v", checkpoint, provider.drafts, provider.thread, provider.last, err)
	}
	delivery, err := service.SendDraft(context.Background(), userID, accountID, draft.ID, checkpoint.LocalRevision, "send-fixture-key-0001")
	if err != nil || delivery.Status != DeliverySent || provider.sends != 1 {
		t.Fatalf("delivery=%+v sends=%d error=%v", delivery, provider.sends, err)
	}
	repeated, err := service.SendDraft(context.Background(), userID, accountID, draft.ID, checkpoint.LocalRevision, "send-fixture-key-0001")
	if err != nil || repeated.ID != delivery.ID || provider.sends != 1 {
		t.Fatalf("repeated=%+v sends=%d error=%v", repeated, provider.sends, err)
	}
}

func TestAmbiguousDeliveryCannotBeRepeated(t *testing.T) {
	repository, pool, userID, accountID := draftFixture(t)
	draft := createDraftFixture(t, repository, userID, accountID)
	provider := &outgoingTestProvider{sendErr: errors.New("unknown remote outcome")}
	service, err := NewDeliveryService(pool, repository, outgoingTestResolver{provider}, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.SendDraft(context.Background(), userID, accountID, draft.ID, draft.LocalRevision, "send-fixture-key-0002")
	if err != nil || first.Status != DeliveryAmbiguous {
		t.Fatalf("ambiguous=%+v error=%v", first, err)
	}
	second, err := service.SendDraft(context.Background(), userID, accountID, draft.ID, draft.LocalRevision, "send-fixture-key-0002")
	if err != nil || second.ID != first.ID || provider.sends != 1 {
		t.Fatalf("repeated ambiguous=%+v sends=%d error=%v", second, provider.sends, err)
	}
}

func TestDeliveryIncludesOnlyOwnerScopedCachedAttachments(t *testing.T) {
	repository, pool, userID, accountID := draftFixture(t)
	store, err := cdn.NewStore(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := cdn.NewService(store, dbgen.New(pool), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	object, err := objects.PutAttachment(context.Background(), cdn.PutAttachmentInput{
		UserID: userID, AccountID: accountID, Filename: "fixture.txt", MediaType: "text/plain",
		Source: strings.NewReader("mailflow"), Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := repository.CreateDraft(context.Background(), CreateDraftInput{
		UserID: userID, AccountID: accountID, Now: time.Now().UTC(),
		Content: DraftContentInput{
			Subject: "Attachment", BodyText: "Body", BodyHTML: "<p>Body</p>", Mode: ComposeNew,
			Recipients:  []MessageAddressInput{{Role: AddressTo, Address: "recipient@example.test"}},
			Attachments: []DraftAttachmentInput{{ObjectID: object.ObjectID, Filename: "fixture.txt", MediaType: object.MediaType, SizeBytes: object.SizeBytes}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &outgoingTestProvider{}
	service, err := NewDeliveryService(pool, repository, outgoingTestResolver{provider}, nil, objects)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := service.SendDraft(context.Background(), userID, accountID, draft.ID, draft.LocalRevision, "send-attachment-key-001")
	if err != nil || delivery.Status != DeliverySent || !strings.Contains(provider.last, "multipart/mixed") || !strings.Contains(provider.last, "filename=fixture.txt") || !strings.Contains(provider.last, "bWFpbGZsb3c=") {
		t.Fatalf("attachment delivery=%+v payload=%q error=%v", delivery, provider.last, err)
	}
}

func outgoingSourceFixture(t *testing.T, pool *pgxpool.Pool, userID, accountID string) Message {
	t.Helper()
	repository := NewThreadRepository(pool)
	stamp := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	thread, err := repository.UpsertThread(context.Background(), UpsertThreadInput{UserID: userID, AccountID: accountID, RemoteID: "remote-thread", LastMessageAt: stamp, Category: CategoryPrimary})
	if err != nil {
		t.Fatal(err)
	}
	message, err := repository.UpsertMessage(context.Background(), UpsertMessageInput{UserID: userID, AccountID: accountID, ThreadID: thread.ID, RemoteID: "remote-message", MessageID: "source@example.test", References: []string{"root@example.test"}, Subject: "Fixture", BodyText: "Source", BodyHTML: "<p>Source</p>", SentAt: stamp})
	if err != nil {
		t.Fatal(err)
	}
	return message
}
