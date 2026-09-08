package microsoftoauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
)

const (
	StateTTL       = 10 * time.Minute
	ConsumerTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"
)

var (
	ErrNotConfigured   = errors.New("Microsoft OAuth is not configured")
	ErrInvalidState    = errors.New("Microsoft OAuth state is invalid or expired")
	ErrProvider        = errors.New("Microsoft OAuth provider request failed")
	ErrInvalidToken    = errors.New("Microsoft OAuth returned incomplete credentials")
	ErrWrongProvider   = errors.New("account is not a Microsoft account")
	ErrConsentRevoked  = errors.New("Microsoft consent was revoked")
	ErrReconsentNeeded = errors.New("Microsoft consent must be renewed")
	ErrTenantPolicy    = errors.New("Microsoft tenant policy rejected the application")
	ErrAccessDenied    = errors.New("Microsoft access was denied")
	validTenantID      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
)

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Authority    string
}

func (config Config) NormalizedAuthority() string {
	authority := strings.ToLower(strings.TrimSpace(config.Authority))
	if authority == "" {
		return "common"
	}
	return authority
}

func (config Config) Valid() bool {
	redirect, err := url.Parse(config.RedirectURL)
	authority := config.NormalizedAuthority()
	validAuthority := authority == "common" || authority == "consumers" || authority == "organizations" || validTenantID.MatchString(authority)
	return strings.TrimSpace(config.ClientID) != "" && strings.TrimSpace(config.ClientSecret) != "" && validAuthority && err == nil && redirect.IsAbs() && (redirect.Scheme == "https" || redirect.Hostname() == "127.0.0.1" || redirect.Hostname() == "localhost")
}

type AccountKind string

const (
	AccountConsumer     AccountKind = "consumer"
	AccountOrganization AccountKind = "organization"
)

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
	AccessToken  string      `json:"accessToken"`
	RefreshToken string      `json:"refreshToken"`
	TokenType    string      `json:"tokenType"`
	Scope        string      `json:"scope"`
	Expiry       time.Time   `json:"expiry"`
	TenantID     string      `json:"tenantId"`
	AccountKind  AccountKind `json:"accountKind"`
}

type Identity struct {
	ID                string
	DisplayName       string
	Mail              string
	UserPrincipalName string
}

func (identity Identity) Address() string {
	if strings.TrimSpace(identity.Mail) != "" {
		return strings.TrimSpace(identity.Mail)
	}
	return strings.TrimSpace(identity.UserPrincipalName)
}

type Provider interface {
	AuthorizationURL(string, string, bool) string
	Exchange(context.Context, string, string) (Token, error)
	Refresh(context.Context, string) (Token, error)
	Identity(context.Context, string) (Identity, error)
}

type Accounts interface {
	Connect(context.Context, accounts.CreateInput) (accounts.Account, error)
	Get(context.Context, string, string) (accounts.Account, error)
	Credentials(context.Context, string, string) (json.RawMessage, error)
	ReplaceCredentials(context.Context, string, string, string, map[string]bool, json.RawMessage) (accounts.Account, error)
	MarkError(context.Context, string, string) (accounts.Account, error)
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
	RevocationURL string           `json:"revocationUrl"`
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

func (service *Service) CallbackWithOwner(ctx context.Context, state, code, providerError string) (accounts.Account, string, error) {
	if !service.Configured() {
		return accounts.Account{}, "", ErrNotConfigured
	}
	if state == "" || len(state) > 256 || len(code) > 4096 || len(providerError) > 256 {
		return accounts.Account{}, "", ErrInvalidState
	}
	transaction, err := service.states.Consume(ctx, state)
	if err != nil {
		return accounts.Account{}, "", ErrInvalidState
	}
	if providerError != "" {
		return accounts.Account{}, transaction.UserID, classifyProviderCode(providerError)
	}
	if code == "" {
		return accounts.Account{}, transaction.UserID, ErrInvalidState
	}
	token, err := service.provider.Exchange(ctx, code, transaction.CodeVerifier)
	if err != nil {
		return accounts.Account{}, transaction.UserID, classifyProviderError(err)
	}
	if !validToken(token) {
		return accounts.Account{}, transaction.UserID, ErrInvalidToken
	}
	if !hasRequiredScopes(token.Scope) {
		return accounts.Account{}, transaction.UserID, ErrReconsentNeeded
	}
	identity, err := service.provider.Identity(ctx, token.AccessToken)
	if err != nil || identity.ID == "" || identity.Address() == "" {
		return accounts.Account{}, transaction.UserID, classifyProviderError(err)
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return accounts.Account{}, transaction.UserID, ErrInvalidToken
	}
	account, err := service.accounts.Connect(ctx, accounts.CreateInput{
		UserID: transaction.UserID, Provider: accounts.ProviderMicrosoft,
		RemoteID: token.TenantID + ":" + identity.ID, DisplayName: identity.Address(),
		Credentials: encoded, Capabilities: capabilities(token),
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
	if account.Provider != accounts.ProviderMicrosoft {
		return accounts.Account{}, ErrWrongProvider
	}
	encoded, err := service.accounts.Credentials(ctx, userID, accountID)
	if err != nil {
		return accounts.Account{}, err
	}
	var existing Token
	if json.Unmarshal(encoded, &existing) != nil || existing.RefreshToken == "" {
		_, _ = service.accounts.MarkError(ctx, userID, accountID)
		return accounts.Account{}, ErrInvalidToken
	}
	refreshed, err := service.provider.Refresh(ctx, existing.RefreshToken)
	if err != nil {
		_, _ = service.accounts.MarkError(ctx, userID, accountID)
		return accounts.Account{}, classifyProviderError(err)
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = existing.RefreshToken
	}
	if refreshed.TenantID == "" {
		refreshed.TenantID = existing.TenantID
		refreshed.AccountKind = existing.AccountKind
	}
	if !validToken(refreshed) {
		_, _ = service.accounts.MarkError(ctx, userID, accountID)
		return accounts.Account{}, ErrInvalidToken
	}
	if !hasRequiredScopes(refreshed.Scope) {
		_, _ = service.accounts.MarkError(ctx, userID, accountID)
		return accounts.Account{}, ErrReconsentNeeded
	}
	identity, err := service.provider.Identity(ctx, refreshed.AccessToken)
	if err != nil || identity.Address() == "" {
		_, _ = service.accounts.MarkError(ctx, userID, accountID)
		return accounts.Account{}, classifyProviderError(err)
	}
	encoded, err = json.Marshal(refreshed)
	if err != nil {
		return accounts.Account{}, ErrInvalidToken
	}
	return service.accounts.ReplaceCredentials(ctx, userID, accountID, identity.Address(), capabilities(refreshed), encoded)
}

func (service *Service) Disconnect(ctx context.Context, userID, accountID string) (DisconnectResult, error) {
	if !service.Configured() {
		return DisconnectResult{}, ErrNotConfigured
	}
	account, err := service.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return DisconnectResult{}, err
	}
	if account.Provider != accounts.ProviderMicrosoft {
		return DisconnectResult{}, ErrWrongProvider
	}
	account, err = service.accounts.Disable(ctx, userID, accountID)
	if err != nil {
		return DisconnectResult{}, err
	}
	return DisconnectResult{Account: account, RemoteRevoked: false, RevocationURL: "https://myapps.microsoft.com"}, nil
}

type ProviderError struct{ Code string }

func (err *ProviderError) Error() string { return "Microsoft provider error: " + err.Code }

func classifyProviderError(err error) error {
	if err == nil {
		return ErrProvider
	}
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		return classifyProviderCode(providerError.Code)
	}
	return ErrProvider
}

func classifyProviderCode(code string) error {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_grant":
		return ErrConsentRevoked
	case "consent_required", "interaction_required", "login_required":
		return ErrReconsentNeeded
	case "access_denied":
		return ErrAccessDenied
	case "unauthorized_client", "invalid_client", "authorization_request_denied":
		return ErrTenantPolicy
	default:
		return ErrProvider
	}
}

func validToken(token Token) bool {
	return token.AccessToken != "" && token.RefreshToken != "" && token.TenantID != "" && (token.AccountKind == AccountConsumer || token.AccountKind == AccountOrganization)
}

func hasRequiredScopes(scope string) bool {
	granted := grantedScopes(scope)
	return granted["user.read"] && granted["mail.readwrite"] && granted["mail.send"]
}

func capabilities(token Token) map[string]bool {
	granted := grantedScopes(token.Scope)
	return map[string]bool{
		"drafts":               granted["mail.readwrite"],
		"folders":              granted["mail.readwrite"],
		"search":               granted["mail.readwrite"],
		"send":                 granted["mail.send"],
		"account.consumer":     token.AccountKind == AccountConsumer,
		"account.organization": token.AccountKind == AccountOrganization,
	}
}

func grantedScopes(scope string) map[string]bool {
	granted := make(map[string]bool)
	for _, item := range strings.Fields(scope) {
		normalized := strings.ToLower(strings.TrimSpace(item))
		normalized = strings.TrimPrefix(normalized, "https://graph.microsoft.com/")
		granted[normalized] = true
	}
	return granted
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
