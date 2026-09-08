package sync

import (
	"context"
	"errors"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

var ErrProviderCapabilityUnavailable = errors.New("mail provider capability is unavailable")

type WorkflowProviderResolver struct {
	accounts  SyncAccountLookup
	google    GmailProviderResolver
	microsoft MicrosoftProviderResolver
}

func NewWorkflowProviderResolver(accountStore SyncAccountLookup, google GmailProviderResolver, microsoft MicrosoftProviderResolver) (*WorkflowProviderResolver, error) {
	if accountStore == nil || google == nil || microsoft == nil {
		return nil, errors.New("mail workflow routing requires accounts and provider resolvers")
	}
	return &WorkflowProviderResolver{accounts: accountStore, google: google, microsoft: microsoft}, nil
}

func (resolver *WorkflowProviderResolver) Resolve(ctx context.Context, user, accountID string, capabilities ...string) (mail.AccountProvider, error) {
	account, err := resolver.accounts.Get(ctx, user, accountID)
	if err != nil || account.DisabledAt != nil {
		return nil, errors.New("mail account is unavailable")
	}
	for _, capability := range capabilities {
		if !supportsCapability(account, capability) {
			return nil, ErrProviderCapabilityUnavailable
		}
	}
	switch account.Provider {
	case accounts.ProviderGoogle:
		provider, resolveErr := resolver.google.ResolveGmail(ctx, user, accountID)
		if resolveErr != nil {
			return nil, errors.New("Google mail provider is unavailable")
		}
		full, ok := provider.(mail.AccountProvider)
		if !ok {
			return nil, errors.New("Google mail provider is incomplete")
		}
		return full, nil
	case accounts.ProviderMicrosoft:
		provider, resolveErr := resolver.microsoft.ResolveMicrosoft(ctx, user, accountID)
		if resolveErr != nil {
			return nil, errors.New("Microsoft mail provider is unavailable")
		}
		return provider, nil
	default:
		return nil, ErrProviderCapabilityUnavailable
	}
}

func supportsCapability(account accounts.Account, capability string) bool {
	if enabled, declared := account.Capabilities[capability]; declared {
		return enabled
	}
	// Accounts connected before the capability keys were expanded already
	// received the full provider scopes. Preserve them until the next refresh.
	if account.Provider != accounts.ProviderGoogle && account.Provider != accounts.ProviderMicrosoft {
		return false
	}
	switch capability {
	case "actions", "attachments", "categories", "drafts", "folders", "labels", "search", "send", "threads":
		return true
	default:
		return false
	}
}
