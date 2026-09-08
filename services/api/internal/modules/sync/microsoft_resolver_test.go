package sync

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/microsoftoauth"
)

type microsoftResolverAccounts struct {
	owner       string
	account     accounts.Account
	credentials json.RawMessage
	refreshed   json.RawMessage
}

func (store *microsoftResolverAccounts) Get(_ context.Context, user, accountID string) (accounts.Account, error) {
	if user != store.owner || accountID != store.account.ID {
		return accounts.Account{}, errors.New("not found")
	}
	return store.account, nil
}

func (store *microsoftResolverAccounts) Credentials(_ context.Context, user, accountID string) (json.RawMessage, error) {
	if user != store.owner || accountID != store.account.ID {
		return nil, errors.New("not found")
	}
	if len(store.refreshed) > 0 {
		return append(json.RawMessage(nil), store.refreshed...), nil
	}
	return append(json.RawMessage(nil), store.credentials...), nil
}

type microsoftResolverRefresh struct {
	store     *microsoftResolverAccounts
	calls     int
	user      string
	accountID string
}

func (refresh *microsoftResolverRefresh) Refresh(_ context.Context, user, accountID string) (accounts.Account, error) {
	refresh.calls++
	refresh.user, refresh.accountID = user, accountID
	refresh.store.refreshed, _ = json.Marshal(microsoftoauth.Token{
		AccessToken: "new-access", RefreshToken: "refresh", Expiry: time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC),
		TenantID: microsoftoauth.ConsumerTenant, AccountKind: microsoftoauth.AccountConsumer,
	})
	return refresh.store.account, nil
}

func TestMicrosoftAccountResolverIsOwnerScopedAndRefreshesCredentials(t *testing.T) {
	normalizer, err := mail.NewNormalizer(mail.DefaultMIMEPolicy())
	if err != nil {
		t.Fatal(err)
	}
	owner := "0199ed3b-c950-7000-8000-000000000001"
	accountID := "0199ed3b-c950-7000-8000-000000000055"
	expired, _ := json.Marshal(microsoftoauth.Token{
		AccessToken: "old-access", RefreshToken: "refresh", Expiry: time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
		TenantID: microsoftoauth.ConsumerTenant, AccountKind: microsoftoauth.AccountConsumer,
	})
	store := &microsoftResolverAccounts{owner: owner, account: accounts.Account{ID: accountID, Provider: accounts.ProviderMicrosoft, DisplayName: "Fixture"}, credentials: expired}
	refresh := &microsoftResolverRefresh{store: store}
	resolver, err := NewMicrosoftAccountResolver(store, refresh, nil, normalizer)
	if err != nil {
		t.Fatal(err)
	}
	resolver.now = func() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) }
	provider, err := resolver.ResolveMicrosoft(context.Background(), owner, accountID)
	if err != nil || provider == nil || provider.Kind() != mail.ProviderMicrosoft || refresh.calls != 1 || refresh.user != owner || refresh.accountID != accountID {
		t.Fatalf("provider=%v refresh=%d user=%q account=%q error=%v", provider != nil, refresh.calls, refresh.user, refresh.accountID, err)
	}
	if _, err := resolver.ResolveMicrosoft(context.Background(), "0199ed3b-c950-7000-8000-000000000002", accountID); err == nil || refresh.calls != 1 {
		t.Fatalf("cross-owner resolve error=%v refreshes=%d", err, refresh.calls)
	}
}

func TestMicrosoftAccountResolverRejectsWrongOrDisabledAccount(t *testing.T) {
	normalizer, _ := mail.NewNormalizer(mail.DefaultMIMEPolicy())
	now := time.Now().UTC()
	for _, account := range []accounts.Account{
		{ID: "0199ed3b-c950-7000-8000-000000000055", Provider: accounts.ProviderGoogle},
		{ID: "0199ed3b-c950-7000-8000-000000000055", Provider: accounts.ProviderMicrosoft, DisabledAt: &now},
	} {
		store := &microsoftResolverAccounts{owner: "owner", account: account, credentials: []byte(`{}`)}
		resolver, err := NewMicrosoftAccountResolver(store, &microsoftResolverRefresh{store: store}, nil, normalizer)
		if err != nil {
			t.Fatal(err)
		}
		if provider, err := resolver.ResolveMicrosoft(context.Background(), "owner", account.ID); err == nil || provider != nil {
			t.Fatalf("account %+v resolved as %v, %v", account, provider, err)
		}
	}
}
