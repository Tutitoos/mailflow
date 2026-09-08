package imap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
)

var (
	ErrInvalidConfiguration = errors.New("IMAP configuration is invalid")
	ErrTLSIdentity          = errors.New("mail server TLS identity could not be verified")
	ErrTimeout              = errors.New("mail server connection timed out")
	ErrAuthentication       = errors.New("mail server authentication failed")
	ErrCapability           = errors.New("mail server capabilities could not be discovered")
	ErrProtocol             = errors.New("mail server protocol failed")
	ErrWrongProvider        = errors.New("account is not an IMAP account")
)

type Accounts interface {
	Connect(context.Context, accounts.CreateInput) (accounts.Account, error)
	Get(context.Context, string, string) (accounts.Account, error)
	DisableAndClearCredentials(context.Context, string, string) (accounts.Account, error)
}

type Prober interface {
	Probe(context.Context, ConnectInput) (map[string]bool, error)
}

type Service struct {
	accounts         Accounts
	prober           Prober
	folderRepository FolderRepository
	folderDiscoverer FolderDiscoverer
}

type ProbeResult struct {
	Capabilities map[string]bool `json:"capabilities"`
}

type DisconnectResult struct {
	Account            accounts.Account `json:"account"`
	RemoteRevoked      bool             `json:"remoteRevoked"`
	CredentialsRemoved bool             `json:"credentialsRemoved"`
}

func NewService(accountService Accounts, prober Prober, options ...FolderOption) (*Service, error) {
	if accountService == nil || prober == nil {
		return nil, errors.New("IMAP setup requires accounts and a protocol prober")
	}
	service := &Service{accounts: accountService, prober: prober}
	for _, option := range options {
		if option == nil || option(service) != nil {
			return nil, ErrInvalidConfiguration
		}
	}
	return service, nil
}

func (service *Service) Probe(ctx context.Context, input ConnectInput) (ProbeResult, error) {
	input = normalize(input)
	if !input.valid() {
		return ProbeResult{}, ErrInvalidConfiguration
	}
	capabilities, err := service.prober.Probe(ctx, input)
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Capabilities: boundedCapabilities(capabilities)}, nil
}

func (service *Service) Connect(ctx context.Context, input ConnectInput) (accounts.Account, error) {
	result, err := service.Probe(ctx, input)
	if err != nil {
		return accounts.Account{}, err
	}
	input = normalize(input)
	credentials, err := json.Marshal(storedCredentials{Username: input.Username, Password: input.Password, IMAP: input.IMAP, SMTP: input.SMTP})
	if err != nil {
		return accounts.Account{}, ErrInvalidConfiguration
	}
	return service.accounts.Connect(ctx, accounts.CreateInput{
		UserID: input.UserID, Provider: accounts.ProviderIMAP, RemoteID: remoteID(input), DisplayName: input.DisplayName,
		Capabilities: result.Capabilities, Credentials: credentials,
	})
}

func (service *Service) Disconnect(ctx context.Context, userID, accountID string) (DisconnectResult, error) {
	account, err := service.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return DisconnectResult{}, err
	}
	if account.Provider != accounts.ProviderIMAP {
		return DisconnectResult{}, ErrWrongProvider
	}
	account, err = service.accounts.DisableAndClearCredentials(ctx, userID, accountID)
	if err != nil {
		return DisconnectResult{}, err
	}
	return DisconnectResult{Account: account, CredentialsRemoved: true}, nil
}

func normalize(input ConnectInput) ConnectInput {
	input.UserID = strings.TrimSpace(input.UserID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Username = strings.TrimSpace(input.Username)
	input.IMAP.Host = strings.ToLower(strings.TrimSpace(input.IMAP.Host))
	input.SMTP.Host = strings.ToLower(strings.TrimSpace(input.SMTP.Host))
	return input
}

func remoteID(input ConnectInput) string {
	digest := sha256.Sum256([]byte(strings.ToLower(input.Username) + "\x00" + input.IMAP.Host + "\x00" + input.SMTP.Host))
	return "imap:" + hex.EncodeToString(digest[:])
}

func boundedCapabilities(input map[string]bool) map[string]bool {
	allowed := map[string]bool{
		"imap.tls": true, "imap.starttls": true, "imap.idle": true, "imap.uidplus": true,
		"imap.move": true, "imap.condstore": true, "imap.qresync": true, "imap.utf8_accept": true,
		"smtp.tls": true, "smtp.starttls": true, "smtp.8bitmime": true, "smtp.smtputf8": true,
		"smtp.dsn": true, "smtp.size": true,
	}
	result := make(map[string]bool, len(allowed))
	for key := range allowed {
		if input[key] {
			result[key] = true
		}
	}
	for _, key := range []string{"actions", "attachments", "folders", "search", "send", "threads"} {
		result[key] = true
	}
	result["drafts"] = result["imap.uidplus"]
	result["categories"] = false
	result["labels"] = false
	return result
}
