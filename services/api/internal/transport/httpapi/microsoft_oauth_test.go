package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/microsoftoauth"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
)

type microsoftStates struct {
	items map[string]microsoftoauth.Transaction
}

func (states *microsoftStates) Put(_ context.Context, state string, transaction microsoftoauth.Transaction, _ time.Duration) error {
	if states.items == nil {
		states.items = make(map[string]microsoftoauth.Transaction)
	}
	states.items[state] = transaction
	return nil
}

func (states *microsoftStates) Consume(_ context.Context, state string) (microsoftoauth.Transaction, error) {
	transaction, ok := states.items[state]
	delete(states.items, state)
	if !ok {
		return microsoftoauth.Transaction{}, microsoftoauth.ErrInvalidState
	}
	return transaction, nil
}

type microsoftProvider struct{}

func (*microsoftProvider) AuthorizationURL(state, _ string, _ bool) string {
	return "https://login.microsoftonline.com/common/oauth2/v2.0/authorize?state=" + url.QueryEscape(state)
}
func (*microsoftProvider) Exchange(context.Context, string, string) (microsoftoauth.Token, error) {
	return microsoftToken("sanitized-access"), nil
}
func (*microsoftProvider) Refresh(context.Context, string) (microsoftoauth.Token, error) {
	return microsoftToken("sanitized-refreshed"), nil
}
func (*microsoftProvider) Identity(context.Context, string) (microsoftoauth.Identity, error) {
	return microsoftoauth.Identity{ID: "sanitized-user", Mail: "owner@example.test"}, nil
}

func microsoftToken(access string) microsoftoauth.Token {
	return microsoftoauth.Token{
		AccessToken: access, RefreshToken: "sanitized-refresh", TokenType: "Bearer",
		Scope:  "openid profile email offline_access User.Read Mail.ReadWrite Mail.Send",
		Expiry: time.Now().UTC().Add(time.Hour), TenantID: microsoftoauth.ConsumerTenant,
		AccountKind: microsoftoauth.AccountConsumer,
	}
}

type microsoftAccountStore struct {
	account     accounts.Account
	credentials json.RawMessage
}

func (store *microsoftAccountStore) List(context.Context, string) ([]accounts.Account, error) {
	if store.account.ID == "" {
		return []accounts.Account{}, nil
	}
	return []accounts.Account{store.account}, nil
}
func (store *microsoftAccountStore) Get(_ context.Context, userID, accountID string) (accounts.Account, error) {
	if userID != testUserID || store.account.ID != accountID {
		return accounts.Account{}, accounts.ErrAccountNotFound
	}
	return store.account, nil
}
func (store *microsoftAccountStore) Connect(_ context.Context, input accounts.CreateInput) (accounts.Account, error) {
	store.credentials = append(json.RawMessage(nil), input.Credentials...)
	store.account = accounts.Account{ID: "0199ed3b-c950-7000-8000-000000000054", Provider: input.Provider, RemoteID: input.RemoteID, DisplayName: input.DisplayName, Capabilities: input.Capabilities, SyncState: accounts.SyncPending}
	return store.account, nil
}
func (store *microsoftAccountStore) Credentials(_ context.Context, userID, accountID string) (json.RawMessage, error) {
	if userID != testUserID || store.account.ID != accountID {
		return nil, accounts.ErrAccountNotFound
	}
	return append(json.RawMessage(nil), store.credentials...), nil
}
func (store *microsoftAccountStore) ReplaceCredentials(_ context.Context, userID, accountID, displayName string, capabilities map[string]bool, credentials json.RawMessage) (accounts.Account, error) {
	if userID != testUserID || store.account.ID != accountID {
		return accounts.Account{}, accounts.ErrAccountNotFound
	}
	store.credentials = append(json.RawMessage(nil), credentials...)
	store.account.DisplayName = displayName
	store.account.Capabilities = capabilities
	store.account.SyncState = accounts.SyncPending
	return store.account, nil
}
func (store *microsoftAccountStore) MarkError(context.Context, string, string) (accounts.Account, error) {
	store.account.SyncState = accounts.SyncError
	return store.account, nil
}
func (store *microsoftAccountStore) Disable(_ context.Context, userID, accountID string) (accounts.Account, error) {
	if userID != testUserID || store.account.ID != accountID {
		return accounts.Account{}, accounts.ErrAccountNotFound
	}
	store.account.SyncState = accounts.SyncDisabled
	now := time.Now().UTC()
	store.account.DisabledAt = &now
	return store.account, nil
}

func TestMicrosoftOAuthHTTPFlowIsAuthenticatedAndReplaySafe(t *testing.T) {
	store := &microsoftAccountStore{}
	syncer := &microsoftSyncRequester{}
	service := microsoftoauth.NewService(
		microsoftoauth.Config{ClientID: "installation-client", ClientSecret: "sanitized-secret", RedirectURL: "https://mail.example.test/api/v1/oauth/microsoft/callback"},
		&microsoftStates{}, &microsoftProvider{}, store,
	)
	app, token := authenticatedAdminApp(t, httpapi.Dependencies{Accounts: store, MicrosoftOAuth: service, Sync: syncer})

	start := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/microsoft/start", strings.NewReader(`{"reconsent":true}`))
	start.Header.Set("Content-Type", "application/json")
	authorizeAdmin(start, token)
	response, err := app.Test(start)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("start status=%d err=%v", response.StatusCode, err)
	}
	var started microsoftoauth.StartResult
	if json.NewDecoder(response.Body).Decode(&started) != nil {
		t.Fatal("decode start response")
	}
	parsed, _ := url.Parse(started.AuthorizationURL)
	state := parsed.Query().Get("state")
	response, err = app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/oauth/microsoft/callback?state="+url.QueryEscape(state)+"&code=sanitized-code", nil))
	if err != nil || response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/settings/accounts?microsoft=connected" {
		t.Fatalf("callback status=%d location=%q err=%v", response.StatusCode, response.Header.Get("Location"), err)
	}
	if syncer.calls != 1 || syncer.user == "" || syncer.account != store.account.ID {
		t.Fatalf("initial sync calls=%d user=%q account=%q", syncer.calls, syncer.user, syncer.account)
	}
	response, err = app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/oauth/microsoft/callback?state="+url.QueryEscape(state)+"&code=sanitized-code", nil))
	if err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("replay status=%d err=%v", response.StatusCode, err)
	}

	refresh := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+store.account.ID+"/refresh", nil)
	authorizeAdmin(refresh, token)
	response, err = app.Test(refresh)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("refresh status=%d err=%v", response.StatusCode, err)
	}
	disconnect := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/"+store.account.ID, nil)
	authorizeAdmin(disconnect, token)
	response, err = app.Test(disconnect)
	if err != nil || response.StatusCode != http.StatusOK || store.account.SyncState != accounts.SyncDisabled {
		t.Fatalf("disconnect status=%d state=%s err=%v", response.StatusCode, store.account.SyncState, err)
	}
	var responseBody map[string]any
	if err := json.NewDecoder(response.Body).Decode(&responseBody); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(responseBody)
	if strings.Contains(string(encoded), "sanitized-refresh") || strings.Contains(string(encoded), "sanitized-refreshed") {
		t.Fatal("HTTP response exposed provider credentials")
	}
}

var _ httpapi.AccountLister = (*microsoftAccountStore)(nil)
var _ microsoftoauth.Accounts = (*microsoftAccountStore)(nil)

type microsoftSyncRequester struct {
	calls   int
	user    string
	account string
}

func (*microsoftSyncRequester) Request(context.Context, string, string) (mailflowsync.Run, error) {
	return mailflowsync.Run{}, nil
}

func (syncer *microsoftSyncRequester) StartInitial(_ context.Context, user, account string) (mailflowsync.Run, error) {
	syncer.calls++
	syncer.user, syncer.account = user, account
	return mailflowsync.Run{ID: "0199ed3b-c950-7000-8000-000000000021"}, nil
}
