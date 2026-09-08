package mail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type ActionProvider interface {
	Kind() ProviderKind
	Apply(context.Context, RemoteAction) error
}

type ActionProcessResult struct {
	Processed bool
	Provider  ProviderKind
	Result    string
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

func (processor *ActionProcessor) ProcessNext(ctx context.Context, userID string) (ActionProcessResult, error) {
	claim, err := processor.service.Claim(ctx, userID)
	if errors.Is(err, ErrActionUnavailable) {
		return ActionProcessResult{}, nil
	}
	if err != nil {
		return ActionProcessResult{}, err
	}
	provider, resolveErr := processor.providers.ResolveActionProvider(ctx, userID, claim.AccountID)
	result := ActionProcessResult{Processed: true, Result: "failure"}
	if resolveErr == nil && provider != nil {
		result.Provider = provider.Kind()
	} else if resolveErr == nil {
		resolveErr = errors.New("mail action provider is unavailable")
	}
	remote, targetErr := processor.remote.ResolveRemoteAction(ctx, userID, claim.PendingAction)
	if resolveErr == nil && targetErr == nil {
		targetErr = provider.Apply(ctx, remote)
	}
	if targetErr == nil && resolveErr == nil {
		_, err = processor.service.Complete(ctx, userID, claim)
		result.Result = "success"
		return result, err
	}
	errorCode := "provider_unavailable"
	if targetErr != nil {
		errorCode = "remote_action_failed"
	}
	_, conflict, err := processor.service.Fail(ctx, userID, claim, errorCode, processor.now().Add(time.Duration(claim.Attempts)*time.Minute), claim.AuthoritativeState, func(ctx context.Context, tx pgx.Tx) error {
		return processor.state.ApplyActionState(ctx, tx, EnqueueActionInput{UserID: userID, AccountID: claim.AccountID, Kind: claim.Kind, TargetKind: claim.TargetKind, TargetID: claim.TargetID}, json.RawMessage(claim.AuthoritativeState))
	})
	if err == nil {
		result.Result = "retry"
		if conflict {
			result.Result = "conflict"
		}
	}
	return result, err
}

func (repository *ThreadRepositoryStore) ResolveRemoteAction(ctx context.Context, userID string, action PendingAction) (RemoteAction, error) {
	targetIDs, provider, err := repository.remoteActionTargets(ctx, userID, action)
	if err != nil {
		return RemoteAction{}, err
	}
	remote := RemoteAction{IdempotencyKey: action.IdempotencyKey, Kind: string(action.Kind), TargetKind: string(action.TargetKind), TargetIDs: targetIDs}
	if action.Kind == ActionAddLabel || action.Kind == ActionRemoveLabel {
		if provider == ProviderIMAP {
			return RemoteAction{}, ErrInvalidAction
		}
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

func (repository *ThreadRepositoryStore) remoteActionTargets(ctx context.Context, userID string, action PendingAction) ([]string, ProviderKind, error) {
	userUUID, accountUUID, targetUUID, err := actionIDs(userID, action.AccountID, action.TargetID)
	if err != nil {
		return nil, "", ErrInvalidAction
	}
	var provider string
	if err := repository.pool.QueryRow(ctx, `select provider from accounts where id = $1 and user_id = $2 and disabled_at is null`, accountUUID, userUUID).Scan(&provider); err != nil {
		return nil, "", ErrInvalidAction
	}
	if action.TargetKind == ActionTargetMessage {
		var remoteID string
		err := repository.pool.QueryRow(ctx, `select messages.remote_id from messages
join accounts on accounts.id = messages.account_id
where messages.id = $1 and messages.account_id = $2 and accounts.user_id = $3`, targetUUID, accountUUID, userUUID).Scan(&remoteID)
		if err != nil || strings.TrimSpace(remoteID) == "" {
			return nil, "", ErrInvalidAction
		}
		return []string{remoteID}, ProviderKind(provider), nil
	}
	if action.TargetKind != ActionTargetThread {
		return nil, "", ErrInvalidAction
	}
	thread, err := repository.GetThread(ctx, userID, action.AccountID, action.TargetID)
	if err != nil {
		return nil, "", err
	}
	if ProviderKind(provider) != ProviderIMAP {
		return []string{thread.RemoteID}, ProviderKind(provider), nil
	}
	rows, err := repository.pool.Query(ctx, `select messages.remote_id from messages
join accounts on accounts.id = messages.account_id
where messages.thread_id = $1 and messages.account_id = $2 and accounts.user_id = $3
and messages.deleted_at is null order by messages.sent_at, messages.id`, targetUUID, accountUUID, userUUID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var remoteID string
		if rows.Scan(&remoteID) != nil || strings.TrimSpace(remoteID) == "" {
			return nil, "", ErrInvalidAction
		}
		result = append(result, remoteID)
	}
	if rows.Err() != nil || len(result) == 0 || len(result) > 1000 {
		return nil, "", ErrInvalidAction
	}
	return result, ProviderIMAP, nil
}
