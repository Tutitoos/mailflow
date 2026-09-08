package microsoftoauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	microsoftLoginBase = "https://login.microsoftonline.com/"
	microsoftGraphMe   = "https://graph.microsoft.com/v1.0/me?$select=id,displayName,mail,userPrincipalName"
)

var microsoftScopes = []string{"openid", "profile", "email", "offline_access", "User.Read", "Mail.ReadWrite", "Mail.Send"}

type Client struct {
	config Config
	http   *http.Client
}

func NewClient(config Config, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{config: config, http: client}
}

func (client *Client) endpoint(kind string) string {
	return microsoftLoginBase + url.PathEscape(client.config.NormalizedAuthority()) + "/oauth2/v2.0/" + kind
}

func (client *Client) AuthorizationURL(state, challenge string, reconsent bool) string {
	values := url.Values{
		"client_id": {client.config.ClientID}, "redirect_uri": {client.config.RedirectURL},
		"response_type": {"code"}, "response_mode": {"query"}, "scope": {strings.Join(microsoftScopes, " ")},
		"state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	if reconsent {
		values.Set("prompt", "consent")
	} else {
		values.Set("prompt", "select_account")
	}
	return client.endpoint("authorize") + "?" + values.Encode()
}

func (client *Client) Exchange(ctx context.Context, code, verifier string) (Token, error) {
	return client.token(ctx, url.Values{
		"client_id": {client.config.ClientID}, "client_secret": {client.config.ClientSecret},
		"redirect_uri": {client.config.RedirectURL}, "grant_type": {"authorization_code"},
		"scope": {strings.Join(microsoftScopes, " ")}, "code": {code}, "code_verifier": {verifier},
	})
}

func (client *Client) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	token, err := client.token(ctx, url.Values{
		"client_id": {client.config.ClientID}, "client_secret": {client.config.ClientSecret},
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "scope": {strings.Join(microsoftScopes, " ")},
	})
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	return token, err
}

func (client *Client) token(ctx context.Context, values url.Values) (Token, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint("token"), strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.http.Do(request)
	if err != nil {
		return Token{}, ErrProvider
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&failure)
		if failure.Error == "" {
			return Token{}, ErrProvider
		}
		return Token{}, &ProviderError{Code: failure.Error}
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		ExpiresIn    int64  `json:"expires_in"`
		IDToken      string `json:"id_token"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 128<<10)).Decode(&payload) != nil || payload.AccessToken == "" || payload.ExpiresIn <= 0 {
		return Token{}, ErrInvalidToken
	}
	tenantID, kind := tokenAccount(payload.IDToken)
	return Token{AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken, TokenType: payload.TokenType, Scope: payload.Scope, Expiry: time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second), TenantID: tenantID, AccountKind: kind}, nil
}

func (client *Client) Identity(ctx context.Context, accessToken string) (Identity, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, microsoftGraphMe, nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := client.http.Do(request)
	if err != nil {
		return Identity{}, ErrProvider
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Identity{}, ErrProvider
	}
	var identity struct {
		ID                string `json:"id"`
		DisplayName       string `json:"displayName"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&identity) != nil || identity.ID == "" {
		return Identity{}, ErrProvider
	}
	result := Identity(identity)
	if result.Address() == "" {
		return Identity{}, ErrProvider
	}
	return result, nil
}

// tokenAccount reads only Microsoft-issued response metadata. Mailflow never
// uses these unverified claims for authorization or identity; Graph /me remains
// the authoritative identity lookup.
func tokenAccount(raw string) (string, AccountKind) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > 64<<10 {
		return "", ""
	}
	var claims struct {
		TenantID string `json:"tid"`
	}
	if json.Unmarshal(payload, &claims) != nil || !validTenantID.MatchString(claims.TenantID) {
		return "", ""
	}
	if strings.EqualFold(claims.TenantID, ConsumerTenant) {
		return strings.ToLower(claims.TenantID), AccountConsumer
	}
	return strings.ToLower(claims.TenantID), AccountOrganization
}
