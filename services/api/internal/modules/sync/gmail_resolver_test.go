package sync

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

type resolverAccounts struct {
	account     accounts.Account
	credentials json.RawMessage
	replaced    json.RawMessage
}

func (store *resolverAccounts) Get(context.Context, string, string) (accounts.Account, error) {
	return store.account, nil
}
func (store *resolverAccounts) Credentials(context.Context, string, string) (json.RawMessage, error) {
	return append(json.RawMessage(nil), store.credentials...), nil
}
func (store *resolverAccounts) ReplaceCredentials(_ context.Context, _, _, _ string, _ map[string]bool, value json.RawMessage) (accounts.Account, error) {
	store.replaced = append(json.RawMessage(nil), value...)
	return store.account, nil
}

type resolverTokens struct{ calls int }

func (tokens *resolverTokens) Refresh(_ context.Context, refresh string) (googleoauth.Token, error) {
	tokens.calls++
	return googleoauth.Token{AccessToken: "new-access", RefreshToken: refresh, Expiry: time.Date(2026, 9, 7, 14, 0, 0, 0, time.UTC)}, nil
}

func TestGmailAccountResolverRefreshesExpiredEncryptedCredentials(t *testing.T) {
	normalizer, err := mail.NewNormalizer(mail.DefaultMIMEPolicy())
	if err != nil {
		t.Fatal(err)
	}
	expired, _ := json.Marshal(googleoauth.Token{AccessToken: "old-access", RefreshToken: "refresh", Expiry: time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)})
	store := &resolverAccounts{account: accounts.Account{ID: "0199ed3b-c950-7000-8000-000000000016", Provider: accounts.ProviderGoogle, DisplayName: "Fixture", Capabilities: map[string]bool{"labels": true}}, credentials: expired}
	tokens := &resolverTokens{}
	resolver, err := NewGmailAccountResolver(store, tokens, nil, normalizer)
	if err != nil {
		t.Fatal(err)
	}
	resolver.now = func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
	provider, err := resolver.ResolveGmail(context.Background(), "0199ed3b-c950-7000-8000-000000000001", store.account.ID)
	if err != nil || provider == nil || tokens.calls != 1 || len(store.replaced) == 0 {
		t.Fatalf("resolve provider=%v refreshes=%d replaced=%d error=%v", provider != nil, tokens.calls, len(store.replaced), err)
	}
	var refreshed googleoauth.Token
	if json.Unmarshal(store.replaced, &refreshed) != nil || refreshed.AccessToken != "new-access" || refreshed.RefreshToken != "refresh" {
		t.Fatal("refreshed credentials were not persisted correctly")
	}
}

func TestGmailAccountResolverSharesQuotaOnlyWithinAnAccount(t *testing.T) {
	normalizer, err := mail.NewNormalizer(mail.DefaultMIMEPolicy())
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewGmailAccountResolver(&resolverAccounts{}, &resolverTokens{}, nil, normalizer)
	if err != nil {
		t.Fatal(err)
	}
	first := resolver.quotaLimiter("user-1", "account-1")
	if first != resolver.quotaLimiter("user-1", "account-1") {
		t.Fatal("same account received different quota limiters")
	}
	if first == resolver.quotaLimiter("user-1", "account-2") || first == resolver.quotaLimiter("user-2", "account-1") {
		t.Fatal("different accounts shared a quota limiter")
	}
}
