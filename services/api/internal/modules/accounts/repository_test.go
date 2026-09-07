package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	platformcrypto "github.com/Tutitoos/mailflow/services/api/internal/platform/crypto"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRepositoryEncryptsAndScopesAccounts(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	userID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000016")
	if _, err := pool.Exec(ctx, "insert into users (id, email, name) values ($1, $2, $3)", userID, "owner@example.test", "Owner"); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	vault, err := platformcrypto.NewVault(bytes.Repeat([]byte{23}, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(dbgen.New(pool), vault)
	credentials := json.RawMessage(`{"refreshToken":"provider-secret"}`)

	first, err := repository.Create(ctx, CreateInput{
		UserID: userID.String(), Provider: ProviderGoogle, RemoteID: "google-owner",
		DisplayName: "Personal", Capabilities: map[string]bool{"drafts": true}, Credentials: credentials,
	})
	if err != nil {
		t.Fatalf("create first account: %v", err)
	}
	second, err := repository.Create(ctx, CreateInput{
		UserID: userID.String(), Provider: ProviderMicrosoft, RemoteID: "microsoft-owner",
		DisplayName: "Work", Credentials: json.RawMessage(`{"refreshToken":"second-secret"}`),
	})
	if err != nil {
		t.Fatalf("create second account: %v", err)
	}

	var firstCiphertext, firstNonce, secondNonce []byte
	if err := pool.QueryRow(ctx, "select encrypted_credentials, credential_nonce from accounts where id = $1", first.ID).Scan(&firstCiphertext, &firstNonce); err != nil {
		t.Fatalf("load encrypted credentials: %v", err)
	}
	if err := pool.QueryRow(ctx, "select credential_nonce from accounts where id = $1", second.ID).Scan(&secondNonce); err != nil {
		t.Fatalf("load second nonce: %v", err)
	}
	if bytes.Equal(firstCiphertext, credentials) || bytes.Contains(firstCiphertext, []byte("provider-secret")) {
		t.Fatal("provider credentials were stored as plaintext")
	}
	if bytes.Equal(firstNonce, secondNonce) {
		t.Fatal("account credentials reused an AES-GCM nonce")
	}
	decrypted, err := repository.Credentials(ctx, userID.String(), first.ID)
	if err != nil || !bytes.Equal(decrypted, credentials) {
		t.Fatalf("decrypt credentials: matches=%v error=%v", bytes.Equal(decrypted, credentials), err)
	}

	items, err := repository.List(ctx, userID.String())
	if err != nil || len(items) != 2 {
		t.Fatalf("list accounts: count=%d error=%v", len(items), err)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"provider-secret", "refreshToken", "credentialNonce", "encryptedCredentials"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("public account response contains %q", forbidden)
		}
	}

	if _, err := repository.Create(ctx, CreateInput{
		UserID: userID.String(), Provider: ProviderGoogle, RemoteID: "google-owner",
		DisplayName: "Duplicate", Credentials: json.RawMessage(`{}`),
	}); !errors.Is(err, ErrDuplicateAccount) {
		t.Fatalf("duplicate account error = %v", err)
	}

	otherUser := "0199ed3b-c950-7000-8000-000000000099"
	if _, err := repository.Get(ctx, otherUser, first.ID); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("cross-owner get error = %v", err)
	}
	if _, err := repository.Credentials(ctx, otherUser, first.ID); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("cross-owner credentials error = %v", err)
	}
	if _, err := repository.UpdateCapabilities(ctx, otherUser, first.ID, map[string]bool{"send": true}); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("cross-owner update error = %v", err)
	}
	if _, err := repository.Disable(ctx, otherUser, first.ID); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("cross-owner disable error = %v", err)
	}

	disabled, err := repository.Disable(ctx, userID.String(), first.ID)
	if err != nil || disabled.DisabledAt == nil || disabled.SyncState != SyncDisabled {
		t.Fatalf("disable account: account=%+v error=%v", disabled, err)
	}
	if _, err := repository.UpdateCapabilities(ctx, userID.String(), first.ID, map[string]bool{"send": true}); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("disabled account update error = %v", err)
	}
	reconnected, err := NewService(repository).Connect(ctx, CreateInput{
		UserID: userID.String(), Provider: ProviderGoogle, RemoteID: "google-owner", DisplayName: "Reconnected",
		Capabilities: map[string]bool{"send": true}, Credentials: json.RawMessage(`{"refreshToken":"replacement-secret"}`),
	})
	if err != nil || reconnected.ID != first.ID || reconnected.DisabledAt != nil || reconnected.SyncState != SyncPending {
		t.Fatalf("reconnect account: account=%+v error=%v", reconnected, err)
	}
	replacement, err := repository.Credentials(ctx, userID.String(), first.ID)
	if err != nil || !bytes.Contains(replacement, []byte("replacement-secret")) || bytes.Contains(replacement, []byte("provider-secret")) {
		t.Fatalf("replacement credentials were not stored safely: %v", err)
	}
}
