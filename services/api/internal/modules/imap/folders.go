package imap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

const maxDiscoveredFolders = 5000

type FolderCursorState string

const (
	FolderCursorActive         FolderCursorState = "active"
	FolderCursorResyncRequired FolderCursorState = "resync_required"
	FolderCursorNotSelectable  FolderCursorState = "not_selectable"
	FolderCursorMissing        FolderCursorState = "missing"
)

var (
	ErrFolderDiscovery        = errors.New("IMAP folder discovery failed")
	ErrFolderLimit            = errors.New("IMAP folder limit exceeded")
	ErrFolderIdentityConflict = errors.New("IMAP folder identity is ambiguous")
	ErrFolderPersistence      = errors.New("IMAP folder state could not be persisted")
)

type DiscoveredFolder struct {
	IdentityKey     string
	WireName        string
	Name            string
	NamespacePrefix string
	Delimiter       string
	Role            mail.MailboxRole
	Selectable      bool
	Subscribed      bool
	UIDNext         *int64
	UIDValidity     *int64
	TotalCount      int32
	UnreadCount     int32
	Attributes      []string
}

type FolderState struct {
	MailboxID          string            `json:"mailboxId"`
	RemoteID           string            `json:"remoteId"`
	WireName           string            `json:"-"`
	Name               string            `json:"name"`
	Role               mail.MailboxRole  `json:"role"`
	Selectable         bool              `json:"selectable"`
	Subscribed         bool              `json:"subscribed"`
	NamespacePrefix    string            `json:"namespacePrefix"`
	Delimiter          *string           `json:"delimiter"`
	UIDNext            *int64            `json:"uidNext"`
	UIDValidity        *int64            `json:"uidValidity"`
	NextUID            *int64            `json:"nextUid"`
	CursorState        FolderCursorState `json:"cursorState"`
	CursorVersion      int64             `json:"cursorVersion"`
	InvalidatedAt      *time.Time        `json:"invalidatedAt"`
	InvalidationReason *string           `json:"invalidationReason"`
}

type FolderDiscoveryResult struct {
	Folders                []FolderState `json:"folders"`
	ReconciliationRequired bool          `json:"reconciliationRequired"`
}

type FolderDiscoverer interface {
	Discover(context.Context, storedCredentials) ([]DiscoveredFolder, error)
}

type FolderRepository interface {
	Reconcile(context.Context, string, string, []DiscoveredFolder) (FolderDiscoveryResult, error)
}

type credentialAccounts interface {
	Credentials(context.Context, string, string) (json.RawMessage, error)
}

type FolderOption func(*Service) error

func WithFolderDiscovery(repository FolderRepository, discoverer FolderDiscoverer) FolderOption {
	return func(service *Service) error {
		if repository == nil || discoverer == nil {
			return ErrInvalidConfiguration
		}
		service.folderRepository = repository
		service.folderDiscoverer = discoverer
		return nil
	}
}

func (service *Service) DiscoverFolders(ctx context.Context, userID, accountID string) (FolderDiscoveryResult, error) {
	if service.folderRepository == nil || service.folderDiscoverer == nil {
		return FolderDiscoveryResult{}, ErrCapability
	}
	account, err := service.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return FolderDiscoveryResult{}, err
	}
	if account.Provider != accounts.ProviderIMAP {
		return FolderDiscoveryResult{}, ErrWrongProvider
	}
	credentialStore, ok := service.accounts.(credentialAccounts)
	if !ok {
		return FolderDiscoveryResult{}, ErrCapability
	}
	raw, err := credentialStore.Credentials(ctx, userID, accountID)
	if err != nil {
		return FolderDiscoveryResult{}, err
	}
	credentials, err := decodeStoredCredentials(raw)
	if err != nil {
		return FolderDiscoveryResult{}, err
	}
	folders, err := service.folderDiscoverer.Discover(ctx, credentials)
	if err != nil {
		return FolderDiscoveryResult{}, err
	}
	if len(folders) > maxDiscoveredFolders {
		return FolderDiscoveryResult{}, ErrFolderLimit
	}
	if err := normalizeDiscoveredFolders(folders); err != nil {
		return FolderDiscoveryResult{}, err
	}
	return service.folderRepository.Reconcile(ctx, userID, accountID, folders)
}

func decodeStoredCredentials(raw []byte) (storedCredentials, error) {
	var credentials storedCredentials
	if len(raw) == 0 || len(raw) > 64<<10 || json.Unmarshal(raw, &credentials) != nil {
		return storedCredentials{}, ErrInvalidConfiguration
	}
	input := ConnectInput{UserID: "owner", DisplayName: "account", Username: credentials.Username, Password: credentials.Password, IMAP: credentials.IMAP, SMTP: credentials.SMTP}
	if !input.valid() {
		return storedCredentials{}, ErrInvalidConfiguration
	}
	return credentials, nil
}

func normalizeDiscoveredFolders(folders []DiscoveredFolder) error {
	seen := make(map[string]struct{}, len(folders))
	for index := range folders {
		folder := &folders[index]
		folder.Name = strings.TrimSpace(folder.Name)
		folder.WireName = strings.TrimSpace(folder.WireName)
		folder.NamespacePrefix = strings.TrimSpace(folder.NamespacePrefix)
		if folder.Name == "" || folder.WireName == "" || len(folder.Name) > 1024 || len(folder.WireName) > 4096 || len(folder.NamespacePrefix) > 1024 || len(folder.Delimiter) > 1 {
			return ErrFolderDiscovery
		}
		if folder.Selectable && (folder.UIDValidity == nil || *folder.UIDValidity < 1 || folder.UIDNext == nil || *folder.UIDNext < 1) {
			return ErrFolderDiscovery
		}
		folder.Attributes = boundedAttributes(folder.Attributes)
		folder.IdentityKey = folderIdentity(*folder)
		if _, exists := seen[folder.IdentityKey]; exists {
			return ErrFolderIdentityConflict
		}
		seen[folder.IdentityKey] = struct{}{}
	}
	sort.Slice(folders, func(left, right int) bool { return folders[left].IdentityKey < folders[right].IdentityKey })
	return nil
}

func folderIdentity(folder DiscoveredFolder) string {
	if folder.Role != "" {
		return "imap:role:" + string(folder.Role)
	}
	relative := folder.WireName
	if folder.NamespacePrefix != "" && strings.HasPrefix(relative, folder.NamespacePrefix) {
		relative = strings.TrimPrefix(relative, folder.NamespacePrefix)
	}
	if folder.Delimiter != "" {
		relative = strings.ReplaceAll(relative, folder.Delimiter, "/")
	}
	return "imap:path:v1_" + base64.RawURLEncoding.EncodeToString([]byte(relative))
}

func boundedAttributes(attributes []string) []string {
	allowed := map[string]bool{"\\archive": true, "\\all": true, "\\drafts": true, "\\flagged": true, "\\haschildren": true, "\\hasnochildren": true, "\\inbox": true, "\\junk": true, "\\marked": true, "\\noselect": true, "\\sent": true, "\\subscribed": true, "\\trash": true, "\\unmarked": true}
	result := make([]string, 0, len(attributes))
	seen := map[string]bool{}
	for _, attribute := range attributes {
		attribute = strings.ToLower(strings.TrimSpace(attribute))
		if allowed[attribute] && !seen[attribute] {
			result = append(result, attribute)
			seen[attribute] = true
		}
	}
	sort.Strings(result)
	return result
}
