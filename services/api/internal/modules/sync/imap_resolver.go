package sync

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	mailflowimap "github.com/Tutitoos/mailflow/services/api/internal/modules/imap"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/jackc/pgx/v5/pgxpool"
)

type IMAPAccountStore interface {
	Get(context.Context, string, string) (accounts.Account, error)
	Credentials(context.Context, string, string) (json.RawMessage, error)
}

type IMAPProvider interface {
	mail.AccountProvider
}

type IMAPProviderResolver interface {
	ResolveIMAP(context.Context, string, string) (IMAPProvider, error)
}

type IMAPAccountResolver struct {
	accounts   IMAPAccountStore
	store      mailflowimap.ProviderStore
	protocol   mailflowimap.Protocol
	normalizer mail.MIMEMessageNormalizer
}

func NewIMAPAccountResolver(accountStore IMAPAccountStore, pool *pgxpool.Pool, protocol mailflowimap.Protocol, normalizer mail.MIMEMessageNormalizer) (*IMAPAccountResolver, error) {
	if accountStore == nil || pool == nil || normalizer == nil {
		return nil, errors.New("IMAP account resolver configuration is invalid")
	}
	store, err := mailflowimap.NewDatabaseProviderStore(pool)
	if err != nil {
		return nil, err
	}
	if protocol == nil {
		protocol = mailflowimap.DefaultNetworkProtocol()
	}
	return &IMAPAccountResolver{accounts: accountStore, store: store, protocol: protocol, normalizer: normalizer}, nil
}

func (resolver *IMAPAccountResolver) ResolveIMAP(ctx context.Context, user, accountID string) (IMAPProvider, error) {
	account, err := resolver.accounts.Get(ctx, user, accountID)
	if err != nil || account.Provider != accounts.ProviderIMAP || account.DisabledAt != nil {
		return nil, errors.New("IMAP account is unavailable")
	}
	credentials, err := resolver.accounts.Credentials(ctx, user, accountID)
	if err != nil {
		return nil, errors.New("IMAP credentials are unavailable")
	}
	provider, err := mailflowimap.NewProvider(user, accountID, credentials, account.Capabilities, resolver.store, resolver.protocol, resolver.normalizer)
	if err != nil {
		return nil, errors.New("IMAP provider is unavailable")
	}
	return provider, nil
}

var _ IMAPProviderResolver = (*IMAPAccountResolver)(nil)
