package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	platformcrypto "github.com/Tutitoos/mailflow/services/api/internal/platform/crypto"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type RepositoryStore struct {
	queries *dbgen.Queries
	vault   *platformcrypto.Vault
}

func NewRepository(queries *dbgen.Queries, vault *platformcrypto.Vault) *RepositoryStore {
	return &RepositoryStore{queries: queries, vault: vault}
}

func (repository *RepositoryStore) Create(ctx context.Context, input CreateInput) (Account, error) {
	userID, err := parseID(input.UserID)
	if err != nil || !validProvider(input.Provider) || !validText(input.RemoteID, 512) || !validText(input.DisplayName, 256) || !json.Valid(input.Credentials) || len(input.Credentials) > 64<<10 {
		return Account{}, ErrInvalidAccount
	}
	accountID, err := ids.New()
	if err != nil {
		return Account{}, fmt.Errorf("create account ID: %w", err)
	}
	id, _ := parseID(accountID)
	capabilities, err := encodeCapabilities(input.Capabilities)
	if err != nil {
		return Account{}, err
	}
	ciphertext, nonce, err := repository.vault.Encrypt(input.Credentials, associatedData(input.UserID, accountID))
	if err != nil {
		return Account{}, fmt.Errorf("encrypt account credentials: %w", err)
	}
	row, err := repository.queries.CreateAccount(ctx, dbgen.CreateAccountParams{ID: id, UserID: userID, Provider: string(input.Provider), RemoteID: strings.TrimSpace(input.RemoteID), DisplayName: strings.TrimSpace(input.DisplayName), EncryptedCredentials: ciphertext, CredentialNonce: nonce, Capabilities: capabilities})
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "accounts_user_provider_remote_unique" {
		return Account{}, ErrDuplicateAccount
	}
	if err != nil {
		return Account{}, fmt.Errorf("create account: %w", err)
	}
	return mapAccount(row.ID, row.Provider, row.RemoteID, row.DisplayName, row.Capabilities, row.SyncState, row.DisabledAt, row.CreatedAt, row.UpdatedAt)
}

func (repository *RepositoryStore) List(ctx context.Context, user string) ([]Account, error) {
	userID, err := parseID(user)
	if err != nil {
		return nil, ErrInvalidAccount
	}
	rows, err := repository.queries.ListAccountsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	result := make([]Account, 0, len(rows))
	for _, row := range rows {
		account, err := mapAccount(row.ID, row.Provider, row.RemoteID, row.DisplayName, row.Capabilities, row.SyncState, row.DisabledAt, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, account)
	}
	return result, nil
}

func (repository *RepositoryStore) Get(ctx context.Context, user, account string) (Account, error) {
	userID, accountID, err := scopedIDs(user, account)
	if err != nil {
		return Account{}, ErrAccountNotFound
	}
	row, err := repository.queries.GetAccountByUser(ctx, dbgen.GetAccountByUserParams{ID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("get account: %w", err)
	}
	return mapAccount(row.ID, row.Provider, row.RemoteID, row.DisplayName, row.Capabilities, row.SyncState, row.DisabledAt, row.CreatedAt, row.UpdatedAt)
}

func (repository *RepositoryStore) Credentials(ctx context.Context, user, account string) (json.RawMessage, error) {
	userID, accountID, err := scopedIDs(user, account)
	if err != nil {
		return nil, ErrAccountNotFound
	}
	row, err := repository.queries.GetAccountCredentialsByUser(ctx, dbgen.GetAccountCredentialsByUserParams{ID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get account credentials: %w", err)
	}
	plaintext, err := repository.vault.Decrypt(row.EncryptedCredentials, row.CredentialNonce, associatedData(user, account))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(plaintext), nil
}

func (repository *RepositoryStore) UpdateCapabilities(ctx context.Context, user, account string, capabilities map[string]bool) (Account, error) {
	userID, accountID, err := scopedIDs(user, account)
	if err != nil {
		return Account{}, ErrAccountNotFound
	}
	encoded, err := encodeCapabilities(capabilities)
	if err != nil {
		return Account{}, err
	}
	row, err := repository.queries.UpdateAccountCapabilities(ctx, dbgen.UpdateAccountCapabilitiesParams{ID: accountID, UserID: userID, Capabilities: encoded})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("update account capabilities: %w", err)
	}
	return mapAccount(row.ID, row.Provider, row.RemoteID, row.DisplayName, row.Capabilities, row.SyncState, row.DisabledAt, row.CreatedAt, row.UpdatedAt)
}

func (repository *RepositoryStore) Disable(ctx context.Context, user, account string) (Account, error) {
	userID, accountID, err := scopedIDs(user, account)
	if err != nil {
		return Account{}, ErrAccountNotFound
	}
	row, err := repository.queries.DisableAccount(ctx, dbgen.DisableAccountParams{ID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("disable account: %w", err)
	}
	return mapAccount(row.ID, row.Provider, row.RemoteID, row.DisplayName, row.Capabilities, row.SyncState, row.DisabledAt, row.CreatedAt, row.UpdatedAt)
}

func mapAccount(id pgtype.UUID, provider, remoteID, displayName string, encoded []byte, state string, disabled, created, updated pgtype.Timestamptz) (Account, error) {
	var capabilities map[string]bool
	if err := json.Unmarshal(encoded, &capabilities); err != nil {
		return Account{}, fmt.Errorf("decode account capabilities: %w", err)
	}
	var disabledAt *time.Time
	if disabled.Valid {
		value := disabled.Time.UTC()
		disabledAt = &value
	}
	return Account{ID: uuid.UUID(id.Bytes).String(), Provider: Provider(provider), RemoteID: remoteID, DisplayName: displayName, Capabilities: capabilities, SyncState: SyncState(state), DisabledAt: disabledAt, CreatedAt: created.Time.UTC(), UpdatedAt: updated.Time.UTC()}, nil
}

func parseID(value string) (pgtype.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}
func scopedIDs(user, account string) (pgtype.UUID, pgtype.UUID, error) {
	userID, err := parseID(user)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	accountID, err := parseID(account)
	return userID, accountID, err
}
func associatedData(user, account string) []byte {
	return []byte("mailflow-account-credentials:v1:" + user + ":" + account)
}
func validText(value string, limit int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= limit
}
func validProvider(provider Provider) bool {
	return provider == ProviderGoogle || provider == ProviderMicrosoft || provider == ProviderIMAP
}
func encodeCapabilities(capabilities map[string]bool) ([]byte, error) {
	if capabilities == nil {
		capabilities = map[string]bool{}
	}
	encoded, err := json.Marshal(capabilities)
	if err != nil || len(encoded) > 16<<10 {
		return nil, ErrInvalidAccount
	}
	return encoded, nil
}
