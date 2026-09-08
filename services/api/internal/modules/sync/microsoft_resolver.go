package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/microsoftgraph"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/microsoftoauth"
)

type MicrosoftAccountStore interface {
	Get(context.Context, string, string) (accounts.Account, error)
	Credentials(context.Context, string, string) (json.RawMessage, error)
}

type MicrosoftCredentialRefresher interface {
	Refresh(context.Context, string, string) (accounts.Account, error)
}

type MicrosoftAccountResolver struct {
	accounts   MicrosoftAccountStore
	tokens     MicrosoftCredentialRefresher
	http       *http.Client
	normalizer mail.MIMEMessageNormalizer
	now        func() time.Time
}

func NewMicrosoftAccountResolver(accountStore MicrosoftAccountStore, tokens MicrosoftCredentialRefresher, client *http.Client, normalizer mail.MIMEMessageNormalizer) (*MicrosoftAccountResolver, error) {
	if accountStore == nil || tokens == nil || normalizer == nil {
		return nil, errors.New("Microsoft Graph account resolver configuration is invalid")
	}
	return &MicrosoftAccountResolver{accounts: accountStore, tokens: tokens, http: client, normalizer: normalizer, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (resolver *MicrosoftAccountResolver) ResolveMicrosoft(ctx context.Context, user, accountID string) (MicrosoftProvider, error) {
	account, err := resolver.accounts.Get(ctx, user, accountID)
	if err != nil || account.Provider != accounts.ProviderMicrosoft || account.DisabledAt != nil {
		return nil, errors.New("Microsoft account is unavailable")
	}
	token, err := resolver.credentials(ctx, user, accountID)
	if err != nil {
		return nil, err
	}
	if token.AccessToken == "" || !token.Expiry.After(resolver.now().Add(time.Minute)) {
		if _, err := resolver.tokens.Refresh(ctx, user, accountID); err != nil {
			return nil, errors.New("Microsoft credentials could not be refreshed")
		}
		token, err = resolver.credentials(ctx, user, accountID)
		if err != nil {
			return nil, err
		}
	}
	if token.AccessToken == "" || token.RefreshToken == "" {
		return nil, errors.New("Microsoft credentials are invalid")
	}
	provider, err := microsoftgraph.New(token.AccessToken, resolver.http, resolver.normalizer)
	if err != nil {
		return nil, errors.New("Microsoft Graph provider is unavailable")
	}
	return provider, nil
}

func (resolver *MicrosoftAccountResolver) credentials(ctx context.Context, user, accountID string) (microsoftoauth.Token, error) {
	encoded, err := resolver.accounts.Credentials(ctx, user, accountID)
	if err != nil {
		return microsoftoauth.Token{}, errors.New("Microsoft credentials are unavailable")
	}
	var token microsoftoauth.Token
	if json.Unmarshal(encoded, &token) != nil || token.RefreshToken == "" {
		return microsoftoauth.Token{}, errors.New("Microsoft credentials are invalid")
	}
	return token, nil
}
