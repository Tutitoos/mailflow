package sync

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

type workflowAccountLookup struct {
	account accounts.Account
	user    string
	id      string
}

func (lookup *workflowAccountLookup) Get(_ context.Context, user, accountID string) (accounts.Account, error) {
	lookup.user, lookup.id = user, accountID
	return lookup.account, nil
}

type workflowProvider struct{ kind mail.ProviderKind }

func (provider *workflowProvider) Kind() mail.ProviderKind { return provider.kind }
func (*workflowProvider) Capabilities(context.Context) (map[string]bool, error) {
	return map[string]bool{"actions": true}, nil
}
func (*workflowProvider) Changes(context.Context, mail.SyncCursor) (mail.ChangePage, error) {
	return mail.ChangePage{}, nil
}
func (*workflowProvider) Profile(context.Context) (mail.ProviderProfile, error) {
	return mail.ProviderProfile{}, nil
}
func (*workflowProvider) Catalog(context.Context, mail.SyncCursor) (mail.CatalogPage, error) {
	return mail.CatalogPage{}, nil
}
func (*workflowProvider) Backfill(context.Context, mail.SyncCursor, *time.Time, *time.Time, int) (mail.ChangePage, error) {
	return mail.ChangePage{}, nil
}
func (*workflowProvider) Apply(context.Context, mail.RemoteAction) error { return nil }
func (*workflowProvider) SaveDraft(context.Context, mail.OutgoingMessage) (string, error) {
	return "draft", nil
}
func (*workflowProvider) Send(context.Context, mail.OutgoingMessage) (string, error) {
	return "message", nil
}
func (*workflowProvider) DeleteDraft(context.Context, string) error { return nil }
func (*workflowProvider) DownloadAttachment(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("fixture")), nil
}

type workflowGmailResolver struct{ provider GmailProvider }

func (resolver workflowGmailResolver) ResolveGmail(context.Context, string, string) (GmailProvider, error) {
	return resolver.provider, nil
}

type workflowMicrosoftResolver struct{ provider MicrosoftProvider }

func (resolver workflowMicrosoftResolver) ResolveMicrosoft(context.Context, string, string) (MicrosoftProvider, error) {
	return resolver.provider, nil
}

func TestWorkflowResolverRoutesScopedProvidersAndCapabilities(t *testing.T) {
	google := &workflowProvider{kind: mail.ProviderGoogle}
	microsoft := &workflowProvider{kind: mail.ProviderMicrosoft}
	lookup := &workflowAccountLookup{account: accounts.Account{Provider: accounts.ProviderMicrosoft, Capabilities: map[string]bool{"actions": true, "send": true}}}
	resolver, err := NewWorkflowProviderResolver(lookup, workflowGmailResolver{google}, workflowMicrosoftResolver{microsoft})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := resolver.Resolve(context.Background(), "owner", "account", "actions", "send")
	if err != nil || provider.Kind() != mail.ProviderMicrosoft || lookup.user != "owner" || lookup.id != "account" {
		t.Fatalf("Microsoft provider = %v owner=%q account=%q error=%v", provider.Kind(), lookup.user, lookup.id, err)
	}

	lookup.account = accounts.Account{Provider: accounts.ProviderGoogle, Capabilities: map[string]bool{"actions": true}}
	provider, err = resolver.Resolve(context.Background(), "owner", "account", "actions")
	if err != nil || provider.Kind() != mail.ProviderGoogle {
		t.Fatalf("Google provider = %v error=%v", provider.Kind(), err)
	}

	lookup.account.Capabilities["actions"] = false
	if _, err := resolver.Resolve(context.Background(), "owner", "account", "actions"); !errors.Is(err, ErrProviderCapabilityUnavailable) {
		t.Fatalf("disabled capability error = %v", err)
	}

	now := time.Now().UTC()
	lookup.account = accounts.Account{Provider: accounts.ProviderMicrosoft, DisabledAt: &now}
	if _, err := resolver.Resolve(context.Background(), "owner", "account"); err == nil {
		t.Fatal("disabled account was resolved")
	}

	lookup.account = accounts.Account{Provider: accounts.ProviderMicrosoft, Capabilities: map[string]bool{}}
	if _, err := resolver.Resolve(context.Background(), "owner", "account", "attachments"); err != nil {
		t.Fatalf("legacy provider capability was not preserved: %v", err)
	}
}
