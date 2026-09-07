package mail

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type ActionProvider interface {
	Apply(context.Context, RemoteAction) error
}

type ActionProviderResolver interface {
	ResolveActionProvider(context.Context, string, string) (ActionProvider, error)
}

type ActionRemoteResolver interface {
	ResolveRemoteAction(context.Context, string, PendingAction) (RemoteAction, error)
}

type ActionProcessor struct {
	service   *PendingActionService
	providers ActionProviderResolver
	remote    ActionRemoteResolver
	state     ActionStateApplier
	now       func() time.Time
}

func NewActionProcessor(service *PendingActionService, providers ActionProviderResolver, remote ActionRemoteResolver, state ActionStateApplier) (*ActionProcessor, error) {
	if service == nil || providers == nil || remote == nil || state == nil {
		return nil, errors.New("mail action processor configuration is invalid")
	}
	return &ActionProcessor{service: service, providers: providers, remote: remote, state: state, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (processor *ActionProcessor) ProcessNext(ctx context.Context, userID string) (bool, error) {
	claim, err := processor.service.Claim(ctx, userID)
	if errors.Is(err, ErrActionUnavailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	provider, resolveErr := processor.providers.ResolveActionProvider(ctx, userID, claim.AccountID)
	remote, targetErr := processor.remote.ResolveRemoteAction(ctx, userID, claim.PendingAction)
	if resolveErr == nil && targetErr == nil {
		targetErr = provider.Apply(ctx, remote)
	}
	if targetErr == nil && resolveErr == nil {
		_, err = processor.service.Complete(ctx, userID, claim)
		return true, err
	}
	errorCode := "provider_unavailable"
	if targetErr != nil {
		errorCode = "remote_action_failed"
	}
	_, _, err = processor.service.Fail(ctx, userID, claim, errorCode, processor.now().Add(time.Duration(claim.Attempts)*time.Minute), claim.AuthoritativeState, func(ctx context.Context, tx pgx.Tx) error {
		return processor.state.ApplyActionState(ctx, tx, EnqueueActionInput{UserID: userID, AccountID: claim.AccountID, Kind: claim.Kind, TargetKind: claim.TargetKind, TargetID: claim.TargetID}, json.RawMessage(claim.AuthoritativeState))
	})
	return true, err
}

func (repository *ThreadRepositoryStore) ResolveRemoteAction(ctx context.Context, userID string, action PendingAction) (RemoteAction, error) {
	thread, err := repository.GetThread(ctx, userID, action.AccountID, action.TargetID)
	if err != nil {
		return RemoteAction{}, err
	}
	remote := RemoteAction{IdempotencyKey: action.IdempotencyKey, Kind: string(action.Kind), TargetKind: string(action.TargetKind), TargetIDs: []string{thread.RemoteID}}
	if action.Kind == ActionAddLabel || action.Kind == ActionRemoveLabel {
		var desired map[string]any
		if json.Unmarshal(action.DesiredState, &desired) != nil {
			return RemoteAction{}, ErrInvalidAction
		}
		labelID, ok := desired["labelId"].(string)
		if !ok {
			return RemoteAction{}, ErrInvalidAction
		}
		var remoteID string
		err = repository.pool.QueryRow(ctx, `select labels.remote_id from labels join accounts on accounts.id = labels.account_id where labels.id = $1 and labels.account_id = $2 and accounts.user_id = $3`, labelID, action.AccountID, userID).Scan(&remoteID)
		if err != nil || remoteID == "" {
			return RemoteAction{}, ErrInvalidAction
		}
		remote.LabelIDs = []string{remoteID}
	}
	return remote, nil
}
