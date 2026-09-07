package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type PendingActionService struct {
	store     PendingActionStore
	publisher ActionEventPublisher
}

func NewPendingActionService(store PendingActionStore, publisher ActionEventPublisher) *PendingActionService {
	return &PendingActionService{store: store, publisher: publisher}
}

func (service *PendingActionService) Enqueue(ctx context.Context, input EnqueueActionInput, apply ApplyActionState) (PendingAction, bool, error) {
	return service.store.Enqueue(ctx, input, apply)
}

func (service *PendingActionService) Claim(ctx context.Context, userID string) (ActionClaim, error) {
	return service.store.Claim(ctx, userID)
}

func (service *PendingActionService) Complete(ctx context.Context, userID string, claim ActionClaim) (PendingAction, error) {
	return service.store.Complete(ctx, userID, claim)
}

func (service *PendingActionService) Fail(ctx context.Context, userID string, claim ActionClaim, code string, availableAt time.Time, authoritative json.RawMessage, restore ApplyActionState) (PendingAction, bool, error) {
	if claim.Attempts >= claim.MaxAttempts && service.publisher == nil {
		return PendingAction{}, false, ErrActionEvents
	}
	action, conflict, err := service.store.Fail(ctx, userID, claim, code, availableAt, authoritative, restore)
	if err != nil || !conflict {
		return action, conflict, err
	}
	payload, err := json.Marshal(map[string]any{
		"code": "remote_action_conflict", "actionId": action.ID, "accountId": action.AccountID,
		"kind": action.Kind, "targetKind": action.TargetKind,
	})
	if err != nil {
		return action, conflict, fmt.Errorf("encode mail action conflict: %w", err)
	}
	if _, err := service.publisher.Publish(ctx, userID, "mail.changed", payload); err != nil {
		return action, conflict, fmt.Errorf("publish mail action conflict: %w", err)
	}
	return action, conflict, nil
}
