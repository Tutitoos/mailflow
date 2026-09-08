package sync

import (
	"context"
	"errors"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

type SyncAccountLookup interface {
	Get(context.Context, string, string) (accounts.Account, error)
}

type RoutedExecutor struct {
	accounts  SyncAccountLookup
	google    PageExecutor
	microsoft PageExecutor
}

type providerPageError struct {
	provider mail.ProviderKind
	cause    error
}

func (failure *providerPageError) Error() string                   { return "mail provider synchronization failed" }
func (failure *providerPageError) Unwrap() error                   { return failure.cause }
func (failure *providerPageError) ProviderKind() mail.ProviderKind { return failure.provider }
func (failure *providerPageError) RetryDelay() time.Duration       { return retryDelay(failure.cause) }

func NewRoutedExecutor(accountStore SyncAccountLookup, google, microsoft PageExecutor) (*RoutedExecutor, error) {
	if accountStore == nil || google == nil || microsoft == nil {
		return nil, errors.New("sync routing requires accounts and provider executors")
	}
	return &RoutedExecutor{accounts: accountStore, google: google, microsoft: microsoft}, nil
}

func (executor *RoutedExecutor) FetchPage(ctx context.Context, user string, run Run) (SyncPage, error) {
	account, err := executor.accounts.Get(ctx, user, run.AccountID)
	if err != nil || account.DisabledAt != nil {
		return SyncPage{}, errors.New("sync account is unavailable")
	}
	provider := mail.ProviderKind(account.Provider)
	var selected PageExecutor
	switch account.Provider {
	case accounts.ProviderGoogle:
		selected = executor.google
	case accounts.ProviderMicrosoft:
		selected = executor.microsoft
	default:
		return SyncPage{}, &providerPageError{provider: provider, cause: errors.New("mail provider synchronization is unavailable")}
	}
	page, err := selected.FetchPage(ctx, user, run)
	if err != nil {
		return SyncPage{}, &providerPageError{provider: provider, cause: err}
	}
	page.Provider = provider
	return page, nil
}

func providerKindFromError(err error) mail.ProviderKind {
	var failure interface{ ProviderKind() mail.ProviderKind }
	if errors.As(err, &failure) {
		return failure.ProviderKind()
	}
	return "unknown"
}

func retryDelay(err error) time.Duration {
	var hinted interface{ RetryDelay() time.Duration }
	if errors.As(err, &hinted) {
		return hinted.RetryDelay()
	}
	return 0
}
