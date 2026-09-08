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

	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
)

type googleStates struct {
	items map[string]googleoauth.Transaction
}

func (states *googleStates) Put(_ context.Context, state string, transaction googleoauth.Transaction, _ time.Duration) error {
	if states.items == nil {
		states.items = make(map[string]googleoauth.Transaction)
	}
	states.items[state] = transaction
	return nil
}

func (states *googleStates) Consume(_ context.Context, state string) (googleoauth.Transaction, error) {
	transaction, ok := states.items[state]
	delete(states.items, state)
	if !ok {
		return googleoauth.Transaction{}, googleoauth.ErrInvalidState
	}
	return transaction, nil
}

type googleProvider struct{}

func (*googleProvider) AuthorizationURL(state, _ string, _ bool) string {
	return "https://accounts.google.example/authorize?state=" + url.QueryEscape(state)
}
func (*googleProvider) Exchange(context.Context, string, string) (googleoauth.Token, error) {
	return googleoauth.Token{AccessToken: "sanitized-access", RefreshToken: "sanitized-refresh", TokenType: "Bearer"}, nil
}
func (*googleProvider) Refresh(context.Context, string) (googleoauth.Token, error) {
	return googleoauth.Token{}, googleoauth.ErrProvider
}
func (*googleProvider) Identity(context.Context, string) (googleoauth.Identity, error) {
	return googleoauth.Identity{Subject: "sanitized-google-user", Email: "owner@example.test"}, nil
}
func (*googleProvider) Revoke(context.Context, string) error { return nil }

func TestGoogleOAuthDesktopFlowReturnsOnlyFixedDeepLinks(t *testing.T) {
	newApp := func() (*googleoauth.Service, *microsoftAccountStore) {
		store := &microsoftAccountStore{}
		return googleoauth.NewService(
			googleoauth.Config{ClientID: "installation-client", ClientSecret: "sanitized-secret", RedirectURL: "https://mail.example.test/api/v1/oauth/google/callback"},
			&googleStates{}, &googleProvider{}, store,
		), store
	}

	for _, test := range []struct {
		name     string
		callback string
		location string
	}{
		{name: "success", callback: "&code=sanitized-code", location: "mailflow://open/settings/accounts?google=connected&sync=pending"},
		{name: "provider failure", callback: "&error=access_denied", location: "mailflow://open/settings/accounts?google=failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, store := newApp()
			app, token := authenticatedAdminApp(t, httpapi.Dependencies{Accounts: store, GoogleOAuth: service})
			start := httptest.NewRequest(http.MethodPost, "/api/v1/oauth/google/start", strings.NewReader(`{"desktop":true}`))
			start.Header.Set("Content-Type", "application/json")
			authorizeAdmin(start, token)
			response, err := app.Test(start)
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("desktop start status=%d err=%v", response.StatusCode, err)
			}
			var started googleoauth.StartResult
			if json.NewDecoder(response.Body).Decode(&started) != nil {
				t.Fatal("decode desktop start response")
			}
			parsed, _ := url.Parse(started.AuthorizationURL)
			callback := httptest.NewRequest(http.MethodGet, "/api/v1/oauth/google/callback?state="+url.QueryEscape(parsed.Query().Get("state"))+test.callback, nil)
			response, err = app.Test(callback)
			if err != nil || response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != test.location {
				t.Fatalf("desktop callback status=%d location=%q err=%v", response.StatusCode, response.Header.Get("Location"), err)
			}
		})
	}
}
