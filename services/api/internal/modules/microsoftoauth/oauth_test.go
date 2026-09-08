package microsoftoauth

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

type memoryStates struct{ items map[string]Transaction }

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
	refreshError  error
}

func (provider *fakeProvider) AuthorizationURL(state, challenge string, _ bool) string {
	provider.lastChallenge = challenge
	return "https://login.example.test/auth?state=" + url.QueryEscape(state)
}
func (*fakeProvider) Exchange(_ context.Context, code, verifier string) (Token, error) {
	if code != "code" || len(verifier) < 43 {
		return Token{}, ErrProvider
	}
	return fixtureToken("access-secret", "refresh-secret"), nil
}
func (provider *fakeProvider) Refresh(_ context.Context, refresh string) (Token, error) {
	if provider.refreshError != nil {
		return Token{}, provider.refreshError
	}
	if refresh != "refresh-secret" {
		return Token{}, ErrProvider
	}
	return fixtureToken("new-access", "refresh-secret"), nil
}
func (*fakeProvider) Identity(context.Context, string) (Identity, error) {
	return Identity{ID: "graph-user", Mail: "owner@example.test"}, nil
}

type fakeAccounts struct {
	credentials json.RawMessage
	connected   accounts.CreateInput
	markedError bool
	disabled    bool
}

func (store *fakeAccounts) Connect(_ context.Context, input accounts.CreateInput) (accounts.Account, error) {
	store.connected = input
	store.credentials = append(json.RawMessage(nil), input.Credentials...)
	return accounts.Account{ID: "0199ed3b-c950-7000-8000-000000000054", Provider: input.Provider, RemoteID: input.RemoteID, DisplayName: input.DisplayName}, nil
}
func (store *fakeAccounts) Credentials(context.Context, string, string) (json.RawMessage, error) {
	return append(json.RawMessage(nil), store.credentials...), nil
}
func (*fakeAccounts) Get(_ context.Context, _, accountID string) (accounts.Account, error) {
	return accounts.Account{ID: accountID, Provider: accounts.ProviderMicrosoft}, nil
}
func (store *fakeAccounts) ReplaceCredentials(_ context.Context, _, accountID, displayName string, capabilities map[string]bool, credentials json.RawMessage) (accounts.Account, error) {
	store.credentials = append(json.RawMessage(nil), credentials...)
	return accounts.Account{ID: accountID, Provider: accounts.ProviderMicrosoft, DisplayName: displayName, Capabilities: capabilities}, nil
}
func (store *fakeAccounts) MarkError(_ context.Context, _, accountID string) (accounts.Account, error) {
	store.markedError = true
	return accounts.Account{ID: accountID, Provider: accounts.ProviderMicrosoft, SyncState: accounts.SyncError}, nil
}
func (store *fakeAccounts) Disable(_ context.Context, _, accountID string) (accounts.Account, error) {
	store.disabled = true
	return accounts.Account{ID: accountID, Provider: accounts.ProviderMicrosoft, SyncState: accounts.SyncDisabled}, nil
}

func fixtureToken(access, refresh string) Token {
	return Token{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", Scope: strings.Join(microsoftScopes, " "), Expiry: time.Now().UTC().Add(time.Hour), TenantID: ConsumerTenant, AccountKind: AccountConsumer}
}

func TestOAuthConnectsBothAccountKindsWithSingleUseState(t *testing.T) {
	states, provider, accountStore := &memoryStates{}, &fakeProvider{}, &fakeAccounts{}
	service := NewService(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mail.example.test/api/v1/oauth/microsoft/callback", Authority: "common"}, states, provider, accountStore)
	started, err := service.Start(context.Background(), "0199ed3b-c950-7000-8000-000000000001", false)
	if err != nil || provider.lastChallenge == "" || started.AuthorizationURL == "" {
		t.Fatalf("start = %+v, %v", started, err)
	}
	parsed, _ := url.Parse(started.AuthorizationURL)
	state := parsed.Query().Get("state")
	account, ownerID, err := service.CallbackWithOwner(context.Background(), state, "code", "")
	if err != nil || ownerID == "" || account.RemoteID != ConsumerTenant+":graph-user" || accountStore.connected.Provider != accounts.ProviderMicrosoft || !accountStore.connected.Capabilities["account.consumer"] || !accountStore.connected.Capabilities["actions"] || !accountStore.connected.Capabilities["attachments"] {
		t.Fatalf("callback = %+v, input=%+v, err=%v", account, accountStore.connected, err)
	}
	if _, _, err := service.CallbackWithOwner(context.Background(), state, "code", ""); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replayed callback error = %v", err)
	}
}

func TestOAuthProviderFailureConsumesStateAndMapsTenantPolicy(t *testing.T) {
	states, provider, accountStore := &memoryStates{}, &fakeProvider{}, &fakeAccounts{}
	service := NewService(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mail.example.test/callback"}, states, provider, accountStore)
	started, _ := service.Start(context.Background(), "0199ed3b-c950-7000-8000-000000000001", false)
	parsed, _ := url.Parse(started.AuthorizationURL)
	state := parsed.Query().Get("state")
	if _, _, err := service.CallbackWithOwner(context.Background(), state, "", "authorization_request_denied"); !errors.Is(err, ErrTenantPolicy) {
		t.Fatalf("callback error = %v", err)
	}
	if _, _, err := service.CallbackWithOwner(context.Background(), state, "", "authorization_request_denied"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replayed failure = %v", err)
	}
}

func TestRefreshMarksRevokedConsentAndDisconnectsLocally(t *testing.T) {
	encoded, _ := json.Marshal(fixtureToken("access-secret", "refresh-secret"))
	provider := &fakeProvider{refreshError: &ProviderError{Code: "invalid_grant"}}
	accountStore := &fakeAccounts{credentials: encoded}
	service := NewService(Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mail.example.test/callback"}, &memoryStates{}, provider, accountStore)
	if _, err := service.Refresh(context.Background(), "0199ed3b-c950-7000-8000-000000000001", "0199ed3b-c950-7000-8000-000000000054"); !errors.Is(err, ErrConsentRevoked) || !accountStore.markedError {
		t.Fatalf("refresh error=%v marked=%v", err, accountStore.markedError)
	}
	result, err := service.Disconnect(context.Background(), "0199ed3b-c950-7000-8000-000000000001", "0199ed3b-c950-7000-8000-000000000054")
	if err != nil || result.RemoteRevoked || result.RevocationURL == "" || !accountStore.disabled {
		t.Fatalf("disconnect = %+v, %v", result, err)
	}
	encoded, _ = json.Marshal(result)
	if strings.Contains(string(encoded), "refresh-secret") || strings.Contains(string(encoded), "access-secret") {
		t.Fatal("disconnect response exposed provider credentials")
	}
}

func TestConfigurationRestrictsAuthorityAndRedirect(t *testing.T) {
	valid := Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://mail.example.test/callback", Authority: "organizations"}
	if !valid.Valid() {
		t.Fatal("valid organizational authority rejected")
	}
	valid.Authority = "../common"
	if valid.Valid() {
		t.Fatal("unsafe authority accepted")
	}
}

func TestRequiredScopesDoNotExpandSilently(t *testing.T) {
	if hasRequiredScopes("User.Read Mail.ReadWrite") {
		t.Fatal("Mail.Send omission was accepted")
	}
	if !hasRequiredScopes("user.read MAIL.READWRITE mail.send") {
		t.Fatal("case-insensitive required scopes were rejected")
	}
}
