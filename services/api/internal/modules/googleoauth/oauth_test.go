package googleoauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
)

type memoryStates struct {
	items map[string]Transaction
}

func (states *memoryStates) Put(_ context.Context, state string, transaction Transaction, _ time.Duration) error {
	if states.items == nil {
		states.items = make(map[string]Transaction)
	}
	states.items[state] = transaction
	return nil
}
func (states *memoryStates) Consume(_ context.Context, state string) (Transaction, error) {
	transaction, ok := states.items[state]
	delete(states.items, state)
	if !ok {
		return Transaction{}, ErrInvalidState
	}
	return transaction, nil
}

type fakeProvider struct {
	lastChallenge string
	revoked       string
}

func (provider *fakeProvider) AuthorizationURL(state, challenge string, reconsent bool) string {
	provider.lastChallenge = challenge
	return "https://accounts.example.test/auth?state=" + url.QueryEscape(state)
}
func (*fakeProvider) Exchange(_ context.Context, code, verifier string) (Token, error) {
	if code != "code" || len(verifier) < 43 {
		return Token{}, ErrProvider
	}
	return Token{AccessToken: "access-secret", RefreshToken: "refresh-secret", TokenType: "Bearer"}, nil
}
func (*fakeProvider) Refresh(_ context.Context, refresh string) (Token, error) {
	if refresh != "refresh-secret" {
		return Token{}, ErrProvider
	}
	return Token{AccessToken: "new-access", RefreshToken: refresh, TokenType: "Bearer"}, nil
}
func (*fakeProvider) Identity(context.Context, string) (Identity, error) {
	return Identity{Subject: "google-subject", Email: "owner@example.test"}, nil
}
func (provider *fakeProvider) Revoke(_ context.Context, token string) error {
	provider.revoked = token
	return nil
}

type fakeAccounts struct {
	credentials json.RawMessage
	connected   accounts.CreateInput
	disabled    bool
}

func (store *fakeAccounts) Connect(_ context.Context, input accounts.CreateInput) (accounts.Account, error) {
	store.connected = input
	store.credentials = append(json.RawMessage(nil), input.Credentials...)
	return accounts.Account{ID: "0199ed3b-c950-7000-8000-000000000016", Provider: input.Provider, RemoteID: input.RemoteID, DisplayName: input.DisplayName}, nil
}
func (store *fakeAccounts) Credentials(context.Context, string, string) (json.RawMessage, error) {
	return append(json.RawMessage(nil), store.credentials...), nil
}
func (store *fakeAccounts) Get(_ context.Context, _, accountID string) (accounts.Account, error) {
	return accounts.Account{ID: accountID, Provider: accounts.ProviderGoogle}, nil
}
func (store *fakeAccounts) ReplaceCredentials(_ context.Context, _, accountID, displayName string, _ map[string]bool, credentials json.RawMessage) (accounts.Account, error) {
	store.credentials = append(json.RawMessage(nil), credentials...)
	return accounts.Account{ID: accountID, Provider: accounts.ProviderGoogle, DisplayName: displayName}, nil
}
func (store *fakeAccounts) Disable(_ context.Context, _, accountID string) (accounts.Account, error) {
	store.disabled = true
	return accounts.Account{ID: accountID, Provider: accounts.ProviderGoogle, SyncState: accounts.SyncDisabled}, nil
}

func TestOAuthRequiresPKCEAndSingleUseState(t *testing.T) {
	states, provider, accountStore := &memoryStates{}, &fakeProvider{}, &fakeAccounts{}
	service := NewService(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mail.example.test/api/v1/oauth/google/callback"}, states, provider, accountStore)
	started, err := service.Start(context.Background(), "0199ed3b-c950-7000-8000-000000000001", false)
	if err != nil || provider.lastChallenge == "" || started.AuthorizationURL == "" {
		t.Fatalf("start = %+v, %v", started, err)
	}
	parsed, _ := url.Parse(started.AuthorizationURL)
	state := parsed.Query().Get("state")
	account, ownerID, err := service.CallbackWithOwner(context.Background(), state, "code")
	if err != nil || ownerID != "0199ed3b-c950-7000-8000-000000000001" || account.RemoteID != "google-subject" || accountStore.connected.Provider != accounts.ProviderGoogle || !accountStore.connected.Capabilities["actions"] || !accountStore.connected.Capabilities["attachments"] {
		t.Fatalf("callback = %+v, %v", account, err)
	}
	if _, err := service.Callback(context.Background(), state, "code"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replayed callback error = %v", err)
	}
}

func TestOAuthRefreshAndDisconnectNeverReturnCredentials(t *testing.T) {
	provider, accountStore := &fakeProvider{}, &fakeAccounts{credentials: json.RawMessage(`{"accessToken":"access-secret","refreshToken":"refresh-secret","tokenType":"Bearer","expiry":"2026-09-07T00:00:00Z"}`)}
	service := NewService(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mail.example.test/callback"}, &memoryStates{}, provider, accountStore)
	account, err := service.Refresh(context.Background(), "0199ed3b-c950-7000-8000-000000000001", "0199ed3b-c950-7000-8000-000000000016")
	if err != nil || account.DisplayName != "owner@example.test" {
		t.Fatalf("refresh = %+v, %v", account, err)
	}
	result, err := service.Disconnect(context.Background(), "0199ed3b-c950-7000-8000-000000000001", account.ID)
	if err != nil || !result.RemoteRevoked || !accountStore.disabled || provider.revoked != "refresh-secret" {
		t.Fatalf("disconnect = %+v, %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	for _, secret := range []string{"access-secret", "refresh-secret", "new-access"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("response exposed credential %q", secret)
		}
	}
}
