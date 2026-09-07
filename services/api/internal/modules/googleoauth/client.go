package googleoauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	googleAuthorizationEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenEndpoint         = "https://oauth2.googleapis.com/token"
	googleUserInfoEndpoint      = "https://openidconnect.googleapis.com/v1/userinfo"
	googleRevokeEndpoint        = "https://oauth2.googleapis.com/revoke"
)

var googleScopes = []string{"openid", "email", "https://www.googleapis.com/auth/gmail.modify"}

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

func (client *Client) AuthorizationURL(state, challenge string, reconsent bool) string {
	values := url.Values{
		"client_id":              {client.config.ClientID},
		"redirect_uri":           {client.config.RedirectURL},
		"response_type":          {"code"},
		"scope":                  {strings.Join(googleScopes, " ")},
		"state":                  {state},
		"code_challenge":         {challenge},
		"code_challenge_method":  {"S256"},
		"access_type":            {"offline"},
		"include_granted_scopes": {"true"},
	}
	if reconsent {
		values.Set("prompt", "consent")
	}
	return googleAuthorizationEndpoint + "?" + values.Encode()
}

func (client *Client) Exchange(ctx context.Context, code, verifier string) (Token, error) {
	return client.token(ctx, url.Values{
		"client_id": {client.config.ClientID}, "client_secret": {client.config.ClientSecret},
		"redirect_uri": {client.config.RedirectURL}, "grant_type": {"authorization_code"},
		"code": {code}, "code_verifier": {verifier},
	})
}

func (client *Client) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	token, err := client.token(ctx, url.Values{
		"client_id": {client.config.ClientID}, "client_secret": {client.config.ClientSecret},
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken},
	})
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	return token, err
}

func (client *Client) token(ctx context.Context, values url.Values) (Token, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpoint, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.http.Do(request)
	if err != nil {
		return Token{}, ErrProvider
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return Token{}, ErrProvider
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload) != nil || payload.AccessToken == "" {
		return Token{}, ErrInvalidToken
	}
	return Token{AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken, TokenType: payload.TokenType, Expiry: time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second)}, nil
}

func (client *Client) Identity(ctx context.Context, accessToken string) (Identity, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoEndpoint, nil)
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
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&identity) != nil || identity.Subject == "" || identity.Email == "" || !identity.EmailVerified {
		return Identity{}, ErrProvider
	}
	return Identity{Subject: identity.Subject, Email: identity.Email}, nil
}

func (client *Client) Revoke(ctx context.Context, token string) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, googleRevokeEndpoint, strings.NewReader(url.Values{"token": {token}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.http.Do(request)
	if err != nil {
		return ErrProvider
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: revocation rejected", ErrProvider)
	}
	return nil
}
