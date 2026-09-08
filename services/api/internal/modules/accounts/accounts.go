package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type Provider string
type SyncState string

const (
	ProviderGoogle    Provider = "google"
	ProviderMicrosoft Provider = "microsoft"
	ProviderIMAP      Provider = "imap"

	SyncPending  SyncState = "pending"
	SyncSyncing  SyncState = "syncing"
	SyncIdle     SyncState = "idle"
	SyncError    SyncState = "error"
	SyncDisabled SyncState = "disabled"
)

var (
	ErrAccountNotFound  = errors.New("account not found")
	ErrDuplicateAccount = errors.New("remote account already connected")
	ErrInvalidAccount   = errors.New("invalid account")
)

type Account struct {
	ID           string          `json:"id"`
	Provider     Provider        `json:"provider"`
	RemoteID     string          `json:"remoteId"`
	DisplayName  string          `json:"displayName"`
	Capabilities map[string]bool `json:"capabilities"`
	SyncState    SyncState       `json:"syncState"`
	DisabledAt   *time.Time      `json:"disabledAt"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

type CreateInput struct {
	UserID       string
	Provider     Provider
	RemoteID     string
	DisplayName  string
	Capabilities map[string]bool
	Credentials  json.RawMessage
}

type Repository interface {
	Create(context.Context, CreateInput) (Account, error)
	List(context.Context, string) ([]Account, error)
	Get(context.Context, string, string) (Account, error)
	Credentials(context.Context, string, string) (json.RawMessage, error)
	UpdateCapabilities(context.Context, string, string, map[string]bool) (Account, error)
	Disable(context.Context, string, string) (Account, error)
	FindByRemote(context.Context, string, Provider, string) (Account, error)
	ReplaceCredentials(context.Context, string, string, string, map[string]bool, json.RawMessage) (Account, error)
	MarkError(context.Context, string, string) (Account, error)
}

type Service struct{ repository Repository }

func NewService(repository Repository) *Service { return &Service{repository: repository} }

func (service *Service) List(ctx context.Context, userID string) ([]Account, error) {
	return service.repository.List(ctx, userID)
}

func (service *Service) Connect(ctx context.Context, input CreateInput) (Account, error) {
	account, err := service.repository.Create(ctx, input)
	if !errors.Is(err, ErrDuplicateAccount) {
		return account, err
	}
	existing, err := service.repository.FindByRemote(ctx, input.UserID, input.Provider, input.RemoteID)
	if err != nil {
		return Account{}, err
	}
	return service.repository.ReplaceCredentials(ctx, input.UserID, existing.ID, input.DisplayName, input.Capabilities, input.Credentials)
}

func (service *Service) Credentials(ctx context.Context, userID, accountID string) (json.RawMessage, error) {
	return service.repository.Credentials(ctx, userID, accountID)
}

func (service *Service) Get(ctx context.Context, userID, accountID string) (Account, error) {
	return service.repository.Get(ctx, userID, accountID)
}

func (service *Service) ReplaceCredentials(ctx context.Context, userID, accountID, displayName string, capabilities map[string]bool, credentials json.RawMessage) (Account, error) {
	return service.repository.ReplaceCredentials(ctx, userID, accountID, displayName, capabilities, credentials)
}

func (service *Service) MarkError(ctx context.Context, userID, accountID string) (Account, error) {
	return service.repository.MarkError(ctx, userID, accountID)
}

func (service *Service) Disable(ctx context.Context, userID, accountID string) (Account, error) {
	return service.repository.Disable(ctx, userID, accountID)
}
