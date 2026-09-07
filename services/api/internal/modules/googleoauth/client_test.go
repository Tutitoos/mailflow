package googleoauth

import (
	"net/url"
	"strings"
	"testing"
)

func TestAuthorizationURLUsesMinimumScopesPKCEAndExplicitReconsent(t *testing.T) {
	client := NewClient(Config{ClientID: "installation-client", ClientSecret: "secret", RedirectURL: "https://mail.example.test/callback"}, nil)
	parsed, err := url.Parse(client.AuthorizationURL("single-use-state", "pkce-challenge", true))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") != "single-use-state" || query.Get("code_challenge") != "pkce-challenge" || query.Get("code_challenge_method") != "S256" || query.Get("prompt") != "consent" {
		t.Fatalf("unexpected authorization query")
	}
	scopes := strings.Fields(query.Get("scope"))
	if len(scopes) != 3 || scopes[0] != "openid" || scopes[1] != "email" || scopes[2] != "https://www.googleapis.com/auth/gmail.modify" {
		t.Fatalf("unexpected scopes: %v", scopes)
	}
}
