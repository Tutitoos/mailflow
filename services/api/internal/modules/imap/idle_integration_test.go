package imap

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	platformcrypto "github.com/Tutitoos/mailflow/services/api/internal/platform/crypto"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDatabaseWatchAccountsLoadsOnlyActiveIMAPCredentials(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	ownerID := uuid.MustParse("0199ed3b-c950-7000-8000-000000000060")
	if _, err := pool.Exec(ctx, "insert into users (id, email, name) values ($1, $2, $3)", ownerID, "watch-owner@example.test", "Owner"); err != nil {
		t.Fatal(err)
	}
	vault, err := platformcrypto.NewVault(bytes.Repeat([]byte{60}, 32))
	if err != nil {
		t.Fatal(err)
	}
	accountStore := accounts.NewRepository(dbgen.New(pool), vault)
	credentials := storedCredentials{
		Username: "owner@example.test", Password: "app-password",
		IMAP: ServerConfig{Host: "imap.example.test", Port: 993, TLSMode: TLSImplicit},
		SMTP: ServerConfig{Host: "smtp.example.test", Port: 465, TLSMode: TLSImplicit},
	}
	raw, _ := json.Marshal(credentials)
	active, err := accountStore.Create(ctx, accounts.CreateInput{UserID: ownerID.String(), Provider: accounts.ProviderIMAP, RemoteID: "imap:active", DisplayName: "Active", Credentials: raw})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := accountStore.Create(ctx, accounts.CreateInput{UserID: ownerID.String(), Provider: accounts.ProviderIMAP, RemoteID: "imap:disabled", DisplayName: "Disabled", Credentials: raw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accountStore.Disable(ctx, ownerID.String(), disabled.ID); err != nil {
		t.Fatal(err)
	}
	corrupt, err := accountStore.Create(ctx, accounts.CreateInput{UserID: ownerID.String(), Provider: accounts.ProviderIMAP, RemoteID: "imap:corrupt", DisplayName: "Corrupt", Credentials: raw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update accounts set encrypted_credentials = $1 where id = $2", []byte{1}, corrupt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := accountStore.Create(ctx, accounts.CreateInput{UserID: ownerID.String(), Provider: accounts.ProviderGoogle, RemoteID: "google:ignored", DisplayName: "Google", Credentials: json.RawMessage(`{"token":"ignored"}`)}); err != nil {
		t.Fatal(err)
	}
	source, err := NewDatabaseWatchAccounts(pool, accountStore)
	if err != nil {
		t.Fatal(err)
	}
	got, err := source.Active(ctx)
	if err == nil {
		t.Fatal("corrupt account did not report a bounded credential error")
	}
	if len(got) != 1 || got[0].UserID != ownerID.String() || got[0].AccountID != active.ID || got[0].Credentials.Password != credentials.Password {
		t.Fatalf("active watch accounts = %+v", got)
	}
}
