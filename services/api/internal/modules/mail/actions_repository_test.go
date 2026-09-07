package mail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	stdsync "sync"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPendingActionEnqueueIsIdempotentAndOwnerScoped(t *testing.T) {
	repository, pool, userID, accountID := actionFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "create table optimistic_state (id integer primary key, value integer not null)"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into optimistic_state values (1, 0)"); err != nil {
		t.Fatal(err)
	}
	input := EnqueueActionInput{
		UserID: userID, AccountID: accountID, IdempotencyKey: "request-0000000001",
		Kind: ActionMoveToTrash, TargetKind: ActionTargetThread,
		TargetID: "0199ed3b-c950-7000-8000-000000000332", DesiredState: json.RawMessage(`{"trashed":true}`), MaxAttempts: 3,
	}
	action, created, err := repository.Enqueue(ctx, input, func(ctx context.Context, tx pgx.Tx) error {
		_, applyErr := tx.Exec(ctx, "update optimistic_state set value = value + 1 where id = 1")
		return applyErr
	})
	if err != nil || !created || action.Status != ActionPending || action.Attempts != 0 {
		t.Fatalf("enqueue action = %+v, created=%v, error=%v", action, created, err)
	}
	duplicate, created, err := repository.Enqueue(ctx, input, func(context.Context, pgx.Tx) error {
		t.Fatal("duplicate action must not reapply optimistic state")
		return nil
	})
	if err != nil || created || duplicate.ID != action.ID {
		t.Fatalf("duplicate action = %+v, created=%v, error=%v", duplicate, created, err)
	}
	changed := input
	changed.TargetID = "0199ed3b-c950-7000-8000-000000000334"
	if _, _, err := repository.Enqueue(ctx, changed, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed idempotent request error = %v", err)
	}
	unauthorized := input
	unauthorized.IdempotencyKey = "request-0000000002"
	unauthorized.UserID = "0199ed3b-c950-7000-8000-000000000399"
	if _, _, err := repository.Enqueue(ctx, unauthorized, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, ErrActionNotFound) {
		t.Fatalf("cross-owner enqueue error = %v", err)
	}
	invalid := input
	invalid.IdempotencyKey = "request-0000000003"
	invalid.Kind = ActionKind("delete_permanently")
	if _, _, err := repository.Enqueue(ctx, invalid, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("permanent-delete action error = %v", err)
	}
	invalid.Kind = ActionMoveToTrash
	invalid.DesiredState = json.RawMessage(`{"subject":"private"}`)
	if _, _, err := repository.Enqueue(ctx, invalid, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("private desired state error = %v", err)
	}
	rolledBack := input
	rolledBack.IdempotencyKey = "request-0000000004"
	if _, _, err := repository.Enqueue(ctx, rolledBack, func(ctx context.Context, tx pgx.Tx) error {
		if _, applyErr := tx.Exec(ctx, "update optimistic_state set value = 99 where id = 1"); applyErr != nil {
			return applyErr
		}
		return errors.New("private local state failure")
	}); !errors.Is(err, ErrOptimisticUpdate) || strings.Contains(err.Error(), "private") {
		t.Fatalf("optimistic rollback error = %v", err)
	}
	var optimisticUpdates int
	if err := pool.QueryRow(ctx, "select value from optimistic_state where id = 1").Scan(&optimisticUpdates); err != nil || optimisticUpdates != 1 {
		t.Fatalf("optimistic updates = %d, %v", optimisticUpdates, err)
	}
	var rolledBackActions int
	if err := pool.QueryRow(ctx, "select count(*) from pending_actions where idempotency_key = $1", rolledBack.IdempotencyKey).Scan(&rolledBackActions); err != nil || rolledBackActions != 0 {
		t.Fatalf("rolled-back actions = %d, %v", rolledBackActions, err)
	}
}

func TestPendingActionClaimHasOneWinnerAndCompletesOnce(t *testing.T) {
	repository, _, userID, accountID := actionFixture(t)
	ctx := context.Background()
	enqueueAction(t, repository, userID, accountID, "claim-request-0001", 3)

	const contenders = 12
	var wait stdsync.WaitGroup
	claims := make(chan ActionClaim, contenders)
	errorsChannel := make(chan error, contenders)
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			claim, err := repository.Claim(ctx, userID)
			if err != nil {
				errorsChannel <- err
				return
			}
			claims <- claim
		}()
	}
	wait.Wait()
	close(claims)
	close(errorsChannel)
	var winner ActionClaim
	winners := 0
	for claim := range claims {
		winner = claim
		winners++
	}
	if winners != 1 {
		t.Fatalf("claim winners = %d", winners)
	}
	for claimErr := range errorsChannel {
		if !errors.Is(claimErr, ErrActionUnavailable) {
			t.Fatalf("claim error = %v", claimErr)
		}
	}
	completed, err := repository.Complete(ctx, userID, winner)
	if err != nil || completed.Status != ActionCompleted || completed.Attempts != 1 {
		t.Fatalf("complete action = %+v, %v", completed, err)
	}
	if _, err := repository.Complete(ctx, userID, winner); !errors.Is(err, ErrActionClaimLost) {
		t.Fatalf("repeat completion error = %v", err)
	}
}

func TestExhaustedActionRestoresRemoteStateAndPublishesSafeConflict(t *testing.T) {
	repository, pool, userID, accountID := actionFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "create table reconciled_state (id integer primary key, value boolean not null)"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into reconciled_state values (1, true)"); err != nil {
		t.Fatal(err)
	}
	enqueueAction(t, repository, userID, accountID, "retry-request-0001", 2)

	first, err := repository.Claim(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	retrying, conflict, err := repository.Fail(ctx, userID, first, "provider_unavailable", time.Now().Add(time.Minute), nil, func(context.Context, pgx.Tx) error {
		t.Fatal("non-terminal failure must not restore state")
		return nil
	})
	if err != nil || conflict || retrying.Status != ActionRetryWait || retrying.Attempts != 1 {
		t.Fatalf("retry action = %+v, conflict=%v, error=%v", retrying, conflict, err)
	}
	if _, err := pool.Exec(ctx, "update pending_actions set available_at = now() where id = $1", retrying.ID); err != nil {
		t.Fatal(err)
	}
	second, err := repository.Claim(ctx, userID)
	if err != nil || second.Attempts != 2 {
		t.Fatalf("second claim = %+v, %v", second, err)
	}
	publisher := &recordingActionPublisher{}
	service := NewPendingActionService(repository, publisher)
	authoritative := json.RawMessage(`{"trashed":false}`)
	failed, conflict, err := service.Fail(ctx, userID, second, "remote_conflict", time.Time{}, authoritative, func(ctx context.Context, tx pgx.Tx) error {
		_, restoreErr := tx.Exec(ctx, "update reconciled_state set value = false where id = 1")
		return restoreErr
	})
	if err != nil || !conflict || failed.Status != ActionConflict || !jsonEqual(failed.AuthoritativeState, authoritative) {
		t.Fatalf("terminal action = %+v, conflict=%v, error=%v", failed, conflict, err)
	}
	var restored bool
	if err := pool.QueryRow(ctx, "select value from reconciled_state where id = 1").Scan(&restored); err != nil || restored {
		t.Fatalf("restored state = %v, %v", restored, err)
	}
	if publisher.eventType != "mail.changed" || !json.Valid(publisher.payload) || string(publisher.payload) == "" {
		t.Fatalf("conflict event = type %q payload %s", publisher.eventType, publisher.payload)
	}
	var statuses []string
	rows, err := pool.Query(ctx, "select status from pending_action_attempts where action_id = $1 order by attempt", failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, status)
	}
	if len(statuses) != 2 || statuses[0] != "retry" || statuses[1] != "conflict" {
		t.Fatalf("attempt statuses = %v", statuses)
	}
}

type recordingActionPublisher struct {
	eventType string
	payload   json.RawMessage
}

func (publisher *recordingActionPublisher) Publish(_ context.Context, _ string, eventType string, payload json.RawMessage) (events.Envelope, error) {
	publisher.eventType = eventType
	publisher.payload = append(json.RawMessage(nil), payload...)
	return events.Envelope{Type: eventType, Payload: payload}, nil
}

func enqueueAction(t *testing.T, repository *PendingActionRepository, userID, accountID, key string, maxAttempts int) PendingAction {
	t.Helper()
	action, created, err := repository.Enqueue(context.Background(), EnqueueActionInput{
		UserID: userID, AccountID: accountID, IdempotencyKey: key,
		Kind: ActionMarkRead, TargetKind: ActionTargetMessage,
		TargetID: "0199ed3b-c950-7000-8000-000000000333", DesiredState: json.RawMessage(`{"read":true}`), MaxAttempts: maxAttempts,
	}, func(context.Context, pgx.Tx) error { return nil })
	if err != nil || !created {
		t.Fatalf("enqueue fixture action = %+v, created=%v, error=%v", action, created, err)
	}
	return action
}

func actionFixture(t *testing.T) (*PendingActionRepository, *pgxpool.Pool, string, string) {
	t.Helper()
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("migrate action database: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open action database: %v", err)
	}
	t.Cleanup(pool.Close)
	userID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000032")
	accountID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000132")
	if _, err := pool.Exec(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "action-owner@example.test", "Owner"); err != nil {
		t.Fatalf("create action owner: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into accounts (id, user_id, provider, remote_id, display_name, encrypted_credentials, credential_nonce, capabilities) values ($1, $2, 'google', 'action-owner', 'Personal', $3, $4, '{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("create action account: %v", err)
	}
	return NewPendingActionRepository(pool), pool, userID.String(), accountID.String()
}
