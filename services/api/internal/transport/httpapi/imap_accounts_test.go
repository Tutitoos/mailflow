package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	mailflowimap "github.com/Tutitoos/mailflow/services/api/internal/modules/imap"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
)

type imapHTTPStore struct {
	input   accounts.CreateInput
	account accounts.Account
}

func (store *imapHTTPStore) List(context.Context, string) ([]accounts.Account, error) {
	return []accounts.Account{store.account}, nil
}

func (store *imapHTTPStore) Get(_ context.Context, userID, accountID string) (accounts.Account, error) {
	if userID != testUserID || accountID != store.account.ID {
		return accounts.Account{}, accounts.ErrAccountNotFound
	}
	return store.account, nil
}

func (store *imapHTTPStore) Connect(_ context.Context, input accounts.CreateInput) (accounts.Account, error) {
	store.input = input
	store.account = accounts.Account{ID: "0199ed3b-c950-7000-8000-000000000058", Provider: input.Provider, RemoteID: input.RemoteID, DisplayName: input.DisplayName, Capabilities: input.Capabilities, SyncState: accounts.SyncPending}
	return store.account, nil
}

func (store *imapHTTPStore) DisableAndClearCredentials(_ context.Context, userID, accountID string) (accounts.Account, error) {
	if userID != testUserID || accountID != store.account.ID {
		return accounts.Account{}, accounts.ErrAccountNotFound
	}
	store.account.SyncState = accounts.SyncDisabled
	store.account.Capabilities = map[string]bool{}
	return store.account, nil
}

type imapHTTPProber struct{ err error }

func (prober imapHTTPProber) Probe(context.Context, mailflowimap.ConnectInput) (map[string]bool, error) {
	return map[string]bool{"imap.idle": true, "private.transcript": true}, prober.err
}

func TestIMAPHTTPConnectUsesAuthenticatedOwnerAndNeverReturnsCredentials(t *testing.T) {
	store := &imapHTTPStore{}
	service, _ := mailflowimap.NewService(store, imapHTTPProber{})
	app, token := authenticatedAdminApp(t, httpapi.Dependencies{Accounts: store, IMAP: service})
	body := `{"userId":"attacker","displayName":"Personal","username":"owner@example.test","password":"app-password","imap":{"host":"imap.example.test","port":993,"tlsMode":"implicit"},"smtp":{"host":"smtp.example.test","port":587,"tlsMode":"starttls"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/imap", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	authorizeAdmin(request, token)
	response, err := app.Test(request)
	if err != nil || response.StatusCode != http.StatusCreated {
		t.Fatalf("connect status=%d error=%v", response.StatusCode, err)
	}
	if store.input.UserID != testUserID || store.input.Provider != accounts.ProviderIMAP || store.input.Capabilities["private.transcript"] {
		t.Fatalf("unsafe account input: %+v", store.input)
	}
	var responseBody map[string]any
	if json.NewDecoder(response.Body).Decode(&responseBody) != nil {
		t.Fatal("decode response")
	}
	encoded, _ := json.Marshal(responseBody)
	if strings.Contains(string(encoded), "app-password") || strings.Contains(string(encoded), "owner@example.test") {
		t.Fatal("HTTP response exposed credentials")
	}

	disconnect := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/"+store.account.ID, nil)
	authorizeAdmin(disconnect, token)
	response, err = app.Test(disconnect)
	if err != nil || response.StatusCode != http.StatusOK || store.account.SyncState != accounts.SyncDisabled {
		t.Fatalf("disconnect status=%d state=%s error=%v", response.StatusCode, store.account.SyncState, err)
	}
}

func TestIMAPHTTPMapsTLSFailuresWithoutEchoingConfiguration(t *testing.T) {
	store := &imapHTTPStore{}
	service, _ := mailflowimap.NewService(store, imapHTTPProber{err: mailflowimap.ErrTLSIdentity})
	app, token := authenticatedAdminApp(t, httpapi.Dependencies{Accounts: store, IMAP: service})
	body := `{"displayName":"Personal","username":"owner@example.test","password":"secret","imap":{"host":"imap.example.test","port":993,"tlsMode":"implicit"},"smtp":{"host":"smtp.example.test","port":587,"tlsMode":"starttls"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/imap/probe", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	authorizeAdmin(request, token)
	response, err := app.Test(request)
	if err != nil || response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("probe status=%d error=%v", response.StatusCode, err)
	}
	var problem map[string]any
	_ = json.NewDecoder(response.Body).Decode(&problem)
	encoded, _ := json.Marshal(problem)
	if problem["code"] != "mail_tls_identity_failed" || strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "imap.example.test") {
		t.Fatalf("unsafe problem response: %s", encoded)
	}
}

var _ httpapi.AccountLister = (*imapHTTPStore)(nil)
var _ mailflowimap.Accounts = (*imapHTTPStore)(nil)
