package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/jackc/pgx/v5"
)

type routingAccounts struct {
	account accounts.Account
	user    string
	id      string
}

func (store *routingAccounts) Get(_ context.Context, user, account string) (accounts.Account, error) {
	store.user, store.id = user, account
	return store.account, nil
}

type routingPageExecutor struct {
	calls int
	err   error
}

func (executor *routingPageExecutor) FetchPage(context.Context, string, Run) (SyncPage, error) {
	executor.calls++
	return SyncPage{Checkpoint: []byte(`{}`), Apply: func(context.Context, pgx.Tx) error { return nil }}, executor.err
}

func TestRoutedExecutorSelectsOwnerScopedAccountProvider(t *testing.T) {
	user, accountID := "0199ed3b-c950-7000-8000-000000000001", "0199ed3b-c950-7000-8000-000000000016"
	store := &routingAccounts{account: accounts.Account{ID: accountID, Provider: accounts.ProviderMicrosoft}}
	google, microsoft := &routingPageExecutor{}, &routingPageExecutor{}
	executor, err := NewRoutedExecutor(store, google, microsoft, &routingPageExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := executor.FetchPage(context.Background(), user, Run{AccountID: accountID})
	if err != nil || page.Provider != mail.ProviderMicrosoft || google.calls != 0 || microsoft.calls != 1 || store.user != user || store.id != accountID {
		t.Fatalf("route page=%+v calls=%d/%d scope=%q/%q error=%v", page, google.calls, microsoft.calls, store.user, store.id, err)
	}
}

func TestRoutedExecutorPreservesTypedRecoveryWithoutLeakingProviderError(t *testing.T) {
	accountID := "0199ed3b-c950-7000-8000-000000000016"
	store := &routingAccounts{account: accounts.Account{ID: accountID, Provider: accounts.ProviderMicrosoft}}
	microsoft := &routingPageExecutor{err: ErrRemoteCursorInvalid}
	executor, _ := NewRoutedExecutor(store, &routingPageExecutor{}, microsoft, &routingPageExecutor{})
	_, err := executor.FetchPage(context.Background(), "0199ed3b-c950-7000-8000-000000000001", Run{AccountID: accountID})
	if !errors.Is(err, ErrRemoteCursorInvalid) || providerKindFromError(err) != mail.ProviderMicrosoft || err.Error() != "mail provider synchronization failed" {
		t.Fatalf("routed error = %v provider=%q", err, providerKindFromError(err))
	}
}
