package mail

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type processorStore struct {
	claim    ActionClaim
	complete bool
	restored bool
}

func (*processorStore) Enqueue(context.Context, EnqueueActionInput, ApplyActionState) (PendingAction, bool, error) {
	return PendingAction{}, false, nil
}
func (store *processorStore) Claim(context.Context, string) (ActionClaim, error) {
	if store.claim.ID == "" {
		return ActionClaim{}, ErrActionUnavailable
	}
	claim := store.claim
	store.claim = ActionClaim{}
	return claim, nil
}
func (store *processorStore) Complete(context.Context, string, ActionClaim) (PendingAction, error) {
	store.complete = true
	return PendingAction{Status: ActionCompleted}, nil
}
func (store *processorStore) Fail(ctx context.Context, _ string, claim ActionClaim, _ string, _ time.Time, _ json.RawMessage, restore ApplyActionState) (PendingAction, bool, error) {
	if claim.Attempts >= claim.MaxAttempts {
		if err := restore(ctx, nil); err != nil {
			return PendingAction{}, false, err
		}
		store.restored = true
		return PendingAction{Status: ActionConflict}, true, nil
	}
	return PendingAction{Status: ActionRetryWait}, false, nil
}

type processorProvider struct {
	err    error
	action RemoteAction
}

func (*processorProvider) Kind() ProviderKind { return ProviderMicrosoft }

func (provider *processorProvider) Apply(_ context.Context, action RemoteAction) error {
	provider.action = action
	return provider.err
}

type processorProviders struct{ provider *processorProvider }

func (providers processorProviders) ResolveActionProvider(context.Context, string, string) (ActionProvider, error) {
	return providers.provider, nil
}

type processorRemote struct{}

func (processorRemote) ResolveRemoteAction(_ context.Context, _ string, action PendingAction) (RemoteAction, error) {
	return RemoteAction{Kind: string(action.Kind), TargetIDs: []string{"remote-target"}}, nil
}

type processorState struct{ restored *bool }

func (state processorState) ApplyActionState(context.Context, pgx.Tx, EnqueueActionInput, json.RawMessage) error {
	*state.restored = true
	return nil
}

func TestActionProcessorCompletesAndRestoresTerminalFailures(t *testing.T) {
	base := PendingAction{ID: "action", AccountID: "account", Kind: ActionArchive, TargetKind: ActionTargetThread, TargetID: "target", AuthoritativeState: json.RawMessage(`{"archived":false}`), Attempts: 1, MaxAttempts: 1}
	store := &processorStore{claim: ActionClaim{PendingAction: base}}
	provider := &processorProvider{}
	restored := false
	processor, err := NewActionProcessor(NewPendingActionService(store, &recordingActionPublisher{}), processorProviders{provider}, processorRemote{}, processorState{&restored})
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessNext(context.Background(), "owner")
	if err != nil || !result.Processed || result.Provider != ProviderMicrosoft || result.Result != "success" || !store.complete || provider.action.TargetIDs[0] != "remote-target" {
		t.Fatalf("completed action: result=%+v complete=%v remote=%+v error=%v", result, store.complete, provider.action, err)
	}

	provider.err = errors.New("provider failure")
	store.claim = ActionClaim{PendingAction: base}
	result, err = processor.ProcessNext(context.Background(), "owner")
	if err != nil || !result.Processed || result.Result != "conflict" || !store.restored || !restored {
		t.Fatalf("terminal action: result=%+v storeRestore=%v stateRestore=%v error=%v", result, store.restored, restored, err)
	}
}

func TestRemoteActionResolverExpandsIMAPThreadsAndScopesMessageTargets(t *testing.T) {
	_, pool, userID, accountID := actionFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "update accounts set provider = 'imap' where id = $1", accountID); err != nil {
		t.Fatal(err)
	}
	repository := NewThreadRepository(pool)
	stamp := time.Now().UTC()
	thread, err := repository.UpsertThread(ctx, UpsertThreadInput{UserID: userID, AccountID: accountID, RemoteID: "imap-thread", LastMessageAt: stamp, Category: CategoryPrimary})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.UpsertMessage(ctx, UpsertMessageInput{UserID: userID, AccountID: accountID, ThreadID: thread.ID, RemoteID: "imap-message-1", SentAt: stamp, BodyText: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertMessage(ctx, UpsertMessageInput{UserID: userID, AccountID: accountID, ThreadID: thread.ID, RemoteID: "imap-message-2", SentAt: stamp.Add(time.Minute), BodyText: "two"}); err != nil {
		t.Fatal(err)
	}
	threadAction, err := repository.ResolveRemoteAction(ctx, userID, PendingAction{AccountID: accountID, TargetKind: ActionTargetThread, TargetID: thread.ID, Kind: ActionMarkRead, IdempotencyKey: "thread-action-key"})
	if err != nil || len(threadAction.TargetIDs) != 2 || threadAction.TargetIDs[0] != "imap-message-1" || threadAction.TargetIDs[1] != "imap-message-2" {
		t.Fatalf("thread targets=%v error=%v", threadAction.TargetIDs, err)
	}
	messageAction, err := repository.ResolveRemoteAction(ctx, userID, PendingAction{AccountID: accountID, TargetKind: ActionTargetMessage, TargetID: first.ID, Kind: ActionStar, IdempotencyKey: "message-action-key"})
	if err != nil || len(messageAction.TargetIDs) != 1 || messageAction.TargetIDs[0] != "imap-message-1" {
		t.Fatalf("message targets=%v error=%v", messageAction.TargetIDs, err)
	}
}
