package httpapi_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testAudience = "mailflow-api"
	testIssuer   = "https://mailflow.test"
	testUserID   = "0199ed3b-c950-7000-8000-000000000014"
)

type fakeUserResolver struct {
	err  error
	user authbridge.User
}

func (resolver fakeUserResolver) FindBySubject(_ context.Context, subject string) (authbridge.User, error) {
	if resolver.err != nil {
		return authbridge.User{}, resolver.err
	}
	if subject != resolver.user.ID {
		return authbridge.User{}, authbridge.ErrUserNotFound
	}
	return resolver.user, nil
}

type rotatingJWKS struct {
	mu   sync.RWMutex
	kid  string
	key  ed25519.PublicKey
	hits int
}

func (keys *rotatingJWKS) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	keys.mu.Lock()
	defer keys.mu.Unlock()
	keys.hits++
	_ = json.NewEncoder(writer).Encode(map[string]any{"keys": []map[string]string{{
		"alg": "EdDSA", "crv": "Ed25519", "kid": keys.kid, "kty": "OKP",
		"use": "sig", "x": base64.RawURLEncoding.EncodeToString(keys.key),
	}}})
}

func (keys *rotatingJWKS) rotate(kid string, key ed25519.PublicKey) {
	keys.mu.Lock()
	defer keys.mu.Unlock()
	keys.kid = kid
	keys.key = key
}

func (keys *rotatingJWKS) requests() int {
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	return keys.hits
}

func TestAuthenticatedCurrentUserAndJWKSRotation(t *testing.T) {
	publicOne, privateOne, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicTwo, privateTwo, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := &rotatingJWKS{kid: "key-one", key: publicOne}
	server := httptest.NewServer(keys)
	defer server.Close()

	user := authbridge.User{ID: testUserID, Email: "owner@example.test", Locale: "en"}
	app := authenticatedApp(server.URL, fakeUserResolver{user: user})

	assertCurrentUser(t, app, signToken(t, privateOne, "key-one", jwt.RegisteredClaims{
		Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		Issuer: testIssuer, Subject: testUserID,
	}), user)

	keys.rotate("key-two", publicTwo)
	assertCurrentUser(t, app, signToken(t, privateTwo, "key-two", jwt.RegisteredClaims{
		Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		Issuer: testIssuer, Subject: testUserID,
	}), user)
	if keys.requests() < 2 {
		t.Fatalf("expected an unknown kid to refresh JWKS, got %d requests", keys.requests())
	}
}

func TestAuthenticationFailuresAreStableAndRedacted(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := &rotatingJWKS{kid: "current", key: publicKey}
	server := httptest.NewServer(keys)
	defer server.Close()
	user := authbridge.User{ID: testUserID, Email: "owner@example.test", Locale: "es"}

	tests := []struct {
		name     string
		token    string
		resolver authbridge.UserResolver
		status   int
		code     string
	}{
		{name: "missing token", resolver: fakeUserResolver{user: user}, status: http.StatusUnauthorized, code: "authentication_failed"},
		{name: "expired", token: signToken(t, privateKey, "current", jwt.RegisteredClaims{Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)), Issuer: testIssuer, Subject: testUserID}), resolver: fakeUserResolver{user: user}, status: http.StatusUnauthorized, code: "authentication_failed"},
		{name: "wrong issuer", token: signToken(t, privateKey, "current", jwt.RegisteredClaims{Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), Issuer: "https://attacker.test", Subject: testUserID}), resolver: fakeUserResolver{user: user}, status: http.StatusUnauthorized, code: "authentication_failed"},
		{name: "wrong audience", token: signToken(t, privateKey, "current", jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"another-api"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), Issuer: testIssuer, Subject: testUserID}), resolver: fakeUserResolver{user: user}, status: http.StatusUnauthorized, code: "authentication_failed"},
		{name: "missing subject", token: signToken(t, privateKey, "current", jwt.RegisteredClaims{Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), Issuer: testIssuer}), resolver: fakeUserResolver{user: user}, status: http.StatusUnauthorized, code: "authentication_failed"},
		{name: "unknown user", token: signToken(t, privateKey, "current", jwt.RegisteredClaims{Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), Issuer: testIssuer, Subject: testUserID}), resolver: fakeUserResolver{err: authbridge.ErrUserNotFound}, status: http.StatusUnauthorized, code: "authentication_failed"},
		{name: "repository unavailable", token: signToken(t, privateKey, "current", jwt.RegisteredClaims{Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), Issuer: testIssuer, Subject: testUserID}), resolver: fakeUserResolver{err: errors.New("database unavailable")}, status: http.StatusServiceUnavailable, code: "authentication_unavailable"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := authenticatedApp(server.URL, test.resolver)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status {
				t.Fatalf("expected status %d, got %d", test.status, response.StatusCode)
			}
			var problem struct {
				Code      string `json:"code"`
				Detail    string `json:"detail"`
				RequestID string `json:"requestId"`
			}
			if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != test.code || problem.RequestID == "" {
				t.Fatalf("unexpected problem: %+v", problem)
			}
			if (test.token != "" && strings.Contains(problem.Detail, test.token)) || strings.Contains(problem.Detail, "database") {
				t.Fatalf("problem detail exposed sensitive internals: %q", problem.Detail)
			}
		})
	}
}

func authenticatedApp(jwksURL string, users authbridge.UserResolver) *fiber.App {
	registry := metrics.NewRegistry()
	return httpapi.New(httpapi.Dependencies{
		Admin: admin.NewService("test", registry), AuthAudience: testAudience,
		AuthIssuer: testIssuer, AuthJWKSURL: jwksURL, CurrentUsers: users,
		Sentry: mailflowsentry.NewService(1024), Translations: translations.NewCatalog(),
	})
}

func assertCurrentUser(t *testing.T, app *fiber.App, token string, want authbridge.User) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var got authbridge.User
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func signToken(t *testing.T, key ed25519.PrivateKey, kid string, claims jwt.RegisteredClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(fmt.Errorf("sign token: %w", err))
	}
	return signed
}
