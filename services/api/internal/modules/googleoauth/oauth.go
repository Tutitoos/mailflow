package googleoauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
)

const StateTTL = 10 * time.Minute

var (
	ErrNotConfigured = errors.New("google OAuth is not configured")
	ErrInvalidState  = errors.New("google OAuth state is invalid or expired")
	ErrProvider      = errors.New("google OAuth provider request failed")
	ErrInvalidToken  = errors.New("google OAuth returned incomplete credentials")
	ErrWrongProvider = errors.New("account is not a Google account")
)

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func (config Config) Valid() bool {
	redirect, err := url.Parse(config.RedirectURL)
	return config.ClientID != "" && config.ClientSecret != "" && err == nil && redirect.IsAbs() && (redirect.Scheme == "https" || redirect.Hostname() == "127.0.0.1" || redirect.Hostname() == "localhost")
}

type Transaction struct {
	UserID       string `json:"userId"`
	CodeVerifier string `json:"codeVerifier"`
	Reconsent    bool   `json:"reconsent"`
}

type StateStore interface {
	Put(context.Context, string, Transaction, time.Duration) error
	Consume(context.Context, string) (Transaction, error)
}

type Token struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	TokenType    string    `json:"tokenType"`
	Expiry       time.Time `json:"expiry"`
}

type Identity struct {
	Subject string
	Email   string
}

type Provider interface {
	AuthorizationURL(string, string, bool) string
	Exchange(context.Context, string, string) (Token, error)
	Refresh(context.Context, string) (Token, error)
	Identity(context.Context, string) (Identity, error)
	Revoke(context.Context, string) error
}

type Accounts interface {
	Connect(context.Context, accounts.CreateInput) (accounts.Account, error)
	Get(context.Context, string, string) (accounts.Account, error)
	Credentials(context.Context, string, string) (json.RawMessage, error)
	ReplaceCredentials(context.Context, string, string, string, map[string]bool, json.RawMessage) (accounts.Account, error)
	Disable(context.Context, string, string) (accounts.Account, error)
}

type Service struct {
	configured bool
	states     StateStore
	provider   Provider
	accounts   Accounts
}

type StartResult struct {
	AuthorizationURL string    `json:"authorizationUrl"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type DisconnectResult struct {
	Account       accounts.Account `json:"account"`
	RemoteRevoked bool             `json:"remoteRevoked"`
}

func NewService(config Config, states StateStore, provider Provider, accountService Accounts) *Service {
	return &Service{configured: config.Valid() && states != nil && provider != nil && accountService != nil, states: states, provider: provider, accounts: accountService}
}

func (service *Service) Configured() bool { return service != nil && service.configured }

func (service *Service) Start(ctx context.Context, userID string, reconsent bool) (StartResult, error) {
	if !service.Configured() {
		return StartResult{}, ErrNotConfigured
	}
	state, err := randomURLToken(32)
	if err != nil {
		return StartResult{}, fmt.Errorf("create OAuth state: %w", err)
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		return StartResult{}, fmt.Errorf("create PKCE verifier: %w", err)
	}
	if err := service.states.Put(ctx, state, Transaction{UserID: userID, CodeVerifier: verifier, Reconsent: reconsent}, StateTTL); err != nil {
		return StartResult{}, fmt.Errorf("store OAuth state: %w", err)
	}
	expires := time.Now().UTC().Add(StateTTL)
	return StartResult{AuthorizationURL: service.provider.AuthorizationURL(state, codeChallenge(verifier), reconsent), ExpiresAt: expires}, nil
}

func (service *Service) Callback(ctx context.Context, state, code string) (accounts.Account, error) {
	account, _, err := service.CallbackWithOwner(ctx, state, code)
	return account, err
}

func (service *Service) CallbackWithOwner(ctx context.Context, state, code string) (accounts.Account, string, error) {
	if !service.Configured() {
		return accounts.Account{}, "", ErrNotConfigured
	}
	if state == "" || len(state) > 256 || code == "" || len(code) > 4096 {
		return accounts.Account{}, "", ErrInvalidState
	}
	transaction, err := service.states.Consume(ctx, state)
	if err != nil {
		return accounts.Account{}, "", ErrInvalidState
	}
	token, err := service.provider.Exchange(ctx, code, transaction.CodeVerifier)
	if err != nil {
		return accounts.Account{}, "", ErrProvider
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		return accounts.Account{}, "", ErrInvalidToken
	}
	identity, err := service.provider.Identity(ctx, token.AccessToken)
	if err != nil || identity.Subject == "" || identity.Email == "" {
		return accounts.Account{}, "", ErrProvider
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return accounts.Account{}, "", ErrInvalidToken
	}
	account, err := service.accounts.Connect(ctx, accounts.CreateInput{
		UserID: transaction.UserID, Provider: accounts.ProviderGoogle, RemoteID: identity.Subject,
		DisplayName: identity.Email, Credentials: encoded,
		Capabilities: googleCapabilities(),
	})
	return account, transaction.UserID, err
}

func (service *Service) Refresh(ctx context.Context, userID, accountID string) (accounts.Account, error) {
	if !service.Configured() {
		return accounts.Account{}, ErrNotConfigured
	}
	account, err := service.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return accounts.Account{}, err
	}
	if account.Provider != accounts.ProviderGoogle {
		return accounts.Account{}, ErrWrongProvider
	}
	encoded, err := service.accounts.Credentials(ctx, userID, accountID)
	if err != nil {
		return accounts.Account{}, err
	}
	var existing Token
	if json.Unmarshal(encoded, &existing) != nil || existing.RefreshToken == "" {
		return accounts.Account{}, ErrInvalidToken
	}
	refreshed, err := service.provider.Refresh(ctx, existing.RefreshToken)
	if err != nil {
		return accounts.Account{}, ErrProvider
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = existing.RefreshToken
	}
	if refreshed.AccessToken == "" {
		return accounts.Account{}, ErrInvalidToken
	}
	identity, err := service.provider.Identity(ctx, refreshed.AccessToken)
	if err != nil || identity.Email == "" {
		return accounts.Account{}, ErrProvider
	}
	encoded, _ = json.Marshal(refreshed)
	return service.accounts.ReplaceCredentials(ctx, userID, accountID, identity.Email, googleCapabilities(), encoded)
}

func googleCapabilities() map[string]bool {
	return map[string]bool{"actions": true, "attachments": true, "categories": true, "drafts": true, "folders": true, "labels": true, "search": true, "send": true, "threads": true}
}

func (service *Service) Disconnect(ctx context.Context, userID, accountID string) (DisconnectResult, error) {
	if !service.Configured() {
		return DisconnectResult{}, ErrNotConfigured
	}
	account, err := service.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return DisconnectResult{}, err
	}
	if account.Provider != accounts.ProviderGoogle {
		return DisconnectResult{}, ErrWrongProvider
	}
	encoded, err := service.accounts.Credentials(ctx, userID, accountID)
	if err != nil {
		return DisconnectResult{}, err
	}
	var token Token
	_ = json.Unmarshal(encoded, &token)
	revokeToken := token.RefreshToken
	if revokeToken == "" {
		revokeToken = token.AccessToken
	}
	remoteRevoked := revokeToken != "" && service.provider.Revoke(ctx, revokeToken) == nil
	account, err = service.accounts.Disable(ctx, userID, accountID)
	if err != nil {
		return DisconnectResult{}, err
	}
	return DisconnectResult{Account: account, RemoteRevoked: remoteRevoked}, nil
}

func codeChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func randomURLToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
