package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/gmail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

type GmailAccountStore interface {
	Get(context.Context, string, string) (accounts.Account, error)
	Credentials(context.Context, string, string) (json.RawMessage, error)
	ReplaceCredentials(context.Context, string, string, string, map[string]bool, json.RawMessage) (accounts.Account, error)
}

type GmailTokenRefresher interface {
	Refresh(context.Context, string) (googleoauth.Token, error)
}

type GmailAccountResolver struct {
	accounts   GmailAccountStore
	tokens     GmailTokenRefresher
	http       *http.Client
	normalizer mail.MIMEMessageNormalizer
	now        func() time.Time
}

func NewGmailAccountResolver(accountStore GmailAccountStore, tokens GmailTokenRefresher, client *http.Client, normalizer mail.MIMEMessageNormalizer) (*GmailAccountResolver, error) {
	if accountStore == nil || tokens == nil || normalizer == nil {
		return nil, errors.New("Gmail account resolver configuration is invalid")
	}
	return &GmailAccountResolver{accounts: accountStore, tokens: tokens, http: client, normalizer: normalizer, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (resolver *GmailAccountResolver) ResolveGmail(ctx context.Context, user, accountID string) (GmailProvider, error) {
	account, err := resolver.accounts.Get(ctx, user, accountID)
	if err != nil || account.Provider != accounts.ProviderGoogle || account.DisabledAt != nil {
		return nil, errors.New("Gmail account is unavailable")
	}
	credentials, err := resolver.accounts.Credentials(ctx, user, accountID)
	if err != nil {
		return nil, errors.New("Gmail credentials are unavailable")
	}
	var token googleoauth.Token
	if json.Unmarshal(credentials, &token) != nil || token.RefreshToken == "" {
		return nil, errors.New("Gmail credentials are invalid")
	}
	if token.AccessToken == "" || !token.Expiry.After(resolver.now().Add(time.Minute)) {
		token, err = resolver.tokens.Refresh(ctx, token.RefreshToken)
		if err != nil || token.AccessToken == "" || token.RefreshToken == "" {
			return nil, errors.New("Gmail credentials could not be refreshed")
		}
		encoded, marshalErr := json.Marshal(token)
		if marshalErr != nil {
			return nil, errors.New("Gmail credentials are invalid")
		}
		if _, err := resolver.accounts.ReplaceCredentials(ctx, user, accountID, account.DisplayName, account.Capabilities, encoded); err != nil {
			return nil, errors.New("Gmail credentials could not be stored")
		}
	}
	return gmail.New(token.AccessToken, resolver.http, resolver.normalizer)
}
