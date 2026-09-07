package sync

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	stdsync "sync"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCursorAdvancementIsOwnerScopedAndAtomic(t *testing.T) {
	repository, pool, userID, accountID := cursorFixture(t)
	ctx := context.Background()
	created, err := repository.Create(ctx, CreateCursorInput{
		UserID: userID, AccountID: accountID, Kind: CursorGoogleHistory, Value: json.RawMessage(`{"historyId":"10"}`),
	})
	if err != nil || created.Version != 1 || created.Checkpoint != 0 || created.State != CursorActive {
		t.Fatalf("create cursor = %+v, %v", created, err)
	}
	if _, err := repository.Create(ctx, CreateCursorInput{UserID: userID, AccountID: accountID, Kind: CursorGoogleHistory, Value: json.RawMessage(`{"historyId":"10"}`)}); !errors.Is(err, ErrCursorExists) {
		t.Fatalf("duplicate cursor error = %v", err)
	}
	if _, err := repository.Get(ctx, "0199ed3b-c950-7000-8000-000000000099", accountID); !errors.Is(err, ErrCursorNotFound) {
		t.Fatalf("cross-owner cursor error = %v", err)
	}
	if _, err := pool.Exec(ctx, "create table sync_effects (id integer primary key)"); err != nil {
		t.Fatalf("create effects table: %v", err)
	}
	advanced, err := repository.Advance(ctx, AdvanceCursorInput{
		UserID: userID, AccountID: accountID, ExpectedVersion: created.Version, Value: json.RawMessage(`{"historyId":"11"}`),
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, applyErr := tx.Exec(ctx, "insert into sync_effects (id) values (1)")
		return applyErr
	})
	if err != nil || advanced.Version != 2 || advanced.Checkpoint != 1 || advanced.LastSuccessAt == nil {
		t.Fatalf("advance cursor = %+v, %v", advanced, err)
	}
	if _, err := repository.Advance(ctx, AdvanceCursorInput{
		UserID: userID, AccountID: accountID, ExpectedVersion: 1, Value: json.RawMessage(`{"historyId":"stale"}`),
	}, func(context.Context, pgx.Tx) error { t.Fatal("stale callback must not run"); return nil }); !errors.Is(err, ErrCursorConflict) {
		t.Fatalf("stale cursor error = %v", err)
	}
	if _, err := repository.Advance(ctx, AdvanceCursorInput{
		UserID: userID, AccountID: accountID, ExpectedVersion: 2, Value: json.RawMessage(`{"historyId":"private"}`),
	}, func(ctx context.Context, tx pgx.Tx) error {
		if _, applyErr := tx.Exec(ctx, "insert into sync_effects (id) values (2)"); applyErr != nil {
			return applyErr
		}
		return errors.New("private provider response")
	}); !errors.Is(err, ErrApplyChanges) || strings.Contains(err.Error(), "private") {
		t.Fatalf("failed apply error = %v", err)
	}
	current, err := repository.Get(ctx, userID, accountID)
	if err != nil || current.Version != 2 || current.Checkpoint != 1 {
		t.Fatalf("cursor changed after rollback = %+v, %v", current, err)
	}
	var effects int
	if err := pool.QueryRow(ctx, "select count(*) from sync_effects").Scan(&effects); err != nil || effects != 1 {
		t.Fatalf("atomic effects count = %d, %v", effects, err)
	}
	invalidated, err := repository.Invalidate(ctx, userID, accountID, ReasonRemoteCursorInvalid)
	if err != nil || invalidated.State != CursorResyncRequired || invalidated.InvalidationReason == nil || *invalidated.InvalidationReason != ReasonRemoteCursorInvalid {
		t.Fatalf("invalidate cursor = %+v, %v", invalidated, err)
	}
	if _, err := repository.Advance(ctx, AdvanceCursorInput{
		UserID: userID, AccountID: accountID, ExpectedVersion: invalidated.Version, Value: json.RawMessage(`{"historyId":"12"}`),
	}, func(context.Context, pgx.Tx) error { return nil }); !errors.Is(err, ErrResyncRequired) {
		t.Fatalf("advance invalid cursor error = %v", err)
	}
}

func TestConcurrentCursorAdvancementAppliesOneChangeSet(t *testing.T) {
	repository, pool, userID, accountID := cursorFixture(t)
	ctx := context.Background()
	created, err := repository.Create(ctx, CreateCursorInput{
		UserID: userID, AccountID: accountID, Kind: CursorMicrosoftDelta, Value: json.RawMessage(`{"deltaLink":"initial"}`),
	})
	if err != nil {
		t.Fatalf("create concurrent cursor: %v", err)
	}
	if _, err := pool.Exec(ctx, "create table concurrent_sync_effects (id integer primary key)"); err != nil {
		t.Fatalf("create concurrent effects table: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait stdsync.WaitGroup
	for id := 1; id <= 2; id++ {
		id := id
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, advanceErr := repository.Advance(ctx, AdvanceCursorInput{
				UserID: userID, AccountID: accountID, ExpectedVersion: created.Version,
				Value: json.RawMessage(`{"deltaLink":"next"}`),
			}, func(ctx context.Context, tx pgx.Tx) error {
				_, applyErr := tx.Exec(ctx, "insert into concurrent_sync_effects (id) values ($1)", id)
				return applyErr
			})
			results <- advanceErr
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	succeeded, conflicted := 0, 0
	for advanceErr := range results {
		switch {
		case advanceErr == nil:
			succeeded++
		case errors.Is(advanceErr, ErrCursorConflict):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent advance error: %v", advanceErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent results = %d succeeded, %d conflicted", succeeded, conflicted)
	}
	var effects int
	if err := pool.QueryRow(ctx, "select count(*) from concurrent_sync_effects").Scan(&effects); err != nil || effects != 1 {
		t.Fatalf("concurrent effects count = %d, %v", effects, err)
	}
}

func TestUIDValidityChangeRequiresResynchronization(t *testing.T) {
	repository, pool, userID, _ := cursorFixture(t)
	ctx := context.Background()
	accountID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000232")
	if _, err := pool.Exec(ctx, `insert into accounts (id, user_id, provider, remote_id, display_name, encrypted_credentials, credential_nonce, capabilities) values ($1, $2, 'imap', 'imap-owner', 'IMAP', $3, $4, '{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("create IMAP account: %v", err)
	}
	uid := int64(100)
	created, err := repository.Create(ctx, CreateCursorInput{UserID: userID, AccountID: accountID.String(), Kind: CursorIMAPUID, UIDValidity: &uid, Value: json.RawMessage(`{"uid":42}`)})
	if err != nil {
		t.Fatalf("create IMAP cursor: %v", err)
	}
	changed := int64(101)
	result, err := repository.Advance(ctx, AdvanceCursorInput{
		UserID: userID, AccountID: accountID.String(), ExpectedVersion: created.Version, UIDValidity: &changed, Value: json.RawMessage(`{"uid":43}`),
	}, func(context.Context, pgx.Tx) error { t.Fatal("UIDVALIDITY callback must not run"); return nil })
	if !errors.Is(err, ErrResyncRequired) || result.State != CursorResyncRequired || result.InvalidationReason == nil || *result.InvalidationReason != ReasonUIDValidityChanged {
		t.Fatalf("UIDVALIDITY result = %+v, %v", result, err)
	}
}

func cursorFixture(t *testing.T) (*CursorRepository, *pgxpool.Pool, string, string) {
	t.Helper()
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("migrate cursor database: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open cursor database: %v", err)
	}
	t.Cleanup(pool.Close)
	userID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000031")
	accountID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000131")
	if _, err := pool.Exec(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "sync-owner@example.test", "Owner"); err != nil {
		t.Fatalf("create cursor owner: %v", err)
	}
	if _, err := pool.Exec(ctx, `insert into accounts (id, user_id, provider, remote_id, display_name, encrypted_credentials, credential_nonce, capabilities) values ($1, $2, 'google', 'sync-owner', 'Personal', $3, $4, '{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatalf("create cursor account: %v", err)
	}
	return NewCursorRepository(pool), pool, userID.String(), accountID.String()
}
