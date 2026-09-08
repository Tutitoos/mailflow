package microsoftoauth

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestAuthorizationURLUsesMinimumScopesPKCEAndAuthority(t *testing.T) {
	client := NewClient(Config{ClientID: "installation-client", ClientSecret: "secret", RedirectURL: "https://mail.example.test/callback", Authority: "consumers"}, nil)
	parsed, err := url.Parse(client.AuthorizationURL("single-use-state", "pkce-challenge", true))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Path != "/consumers/oauth2/v2.0/authorize" || query.Get("state") != "single-use-state" || query.Get("code_challenge") != "pkce-challenge" || query.Get("code_challenge_method") != "S256" || query.Get("prompt") != "consent" {
		t.Fatalf("unexpected authorization URL: %s", parsed.String())
	}
	if got := strings.Fields(query.Get("scope")); strings.Join(got, " ") != strings.Join(microsoftScopes, " ") {
		t.Fatalf("unexpected scopes: %v", got)
	}
}

func TestTokenAccountDistinguishesConsumerAndOrganization(t *testing.T) {
	encode := func(tenant string) string {
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"tid":"` + tenant + `"}`))
		return "header." + payload + ".signature"
	}
	tenant, kind := tokenAccount(encode(ConsumerTenant))
	if tenant != ConsumerTenant || kind != AccountConsumer {
		t.Fatalf("consumer = %q %q", tenant, kind)
	}
	tenant, kind = tokenAccount(encode("11111111-2222-4333-8444-555555555555"))
	if tenant == "" || kind != AccountOrganization {
		t.Fatalf("organization = %q %q", tenant, kind)
	}
	if tenant, kind = tokenAccount("not-a-token"); tenant != "" || kind != "" {
		t.Fatal("invalid ID token metadata accepted")
	}
}

func TestClientConsumesSanitizedMicrosoftContracts(t *testing.T) {
	tokenFixture, err := os.ReadFile("testdata/token_success.json")
	if err != nil {
		t.Fatal(err)
	}
	identityFixture, err := os.ReadFile("testdata/me.json")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Config{ClientID: "installation-client", ClientSecret: "sanitized-secret", RedirectURL: "https://mail.example.test/callback"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		fixture := tokenFixture
		if request.Method == http.MethodGet {
			fixture = identityFixture
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(fixture)))}, nil
	})})

	token, err := client.Exchange(context.Background(), "sanitized-code", strings.Repeat("v", 64))
	if err != nil || token.AccountKind != AccountConsumer || token.TenantID != ConsumerTenant {
		t.Fatalf("token = %+v, %v", token, err)
	}
	identity, err := client.Identity(context.Background(), token.AccessToken)
	if err != nil || identity.ID != "sanitized-user-id" || identity.Address() != "owner@example.test" {
		t.Fatalf("identity = %+v, %v", identity, err)
	}
}

func TestClientReturnsOnlyTypedProviderError(t *testing.T) {
	fixture, err := os.ReadFile("testdata/token_revoked.json")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Config{ClientID: "installation-client", ClientSecret: "sanitized-secret", RedirectURL: "https://mail.example.test/callback"}, &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(fixture)))}, nil
	})})

	_, err = client.Refresh(context.Background(), "sanitized-refresh")
	var providerError *ProviderError
	if !errors.As(err, &providerError) || providerError.Code != "invalid_grant" || strings.Contains(err.Error(), "detail") {
		t.Fatalf("provider error = %v", err)
	}
}
