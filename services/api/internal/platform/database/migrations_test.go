package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/db/migrations"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestAuthIdentityMigrationPreservesFoundationUser(t *testing.T) {
	databaseURL := os.Getenv("MAILFLOW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MAILFLOW_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	lockConnection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("open migration test lock connection: %v", err)
	}
	defer lockConnection.Close()
	if _, err := lockConnection.ExecContext(ctx, "select pg_advisory_lock(73412916)"); err != nil {
		t.Fatalf("acquire migration test lock: %v", err)
	}
	defer func() {
		_, _ = lockConnection.ExecContext(context.Background(), "select pg_advisory_unlock(73412916)")
	}()
	var databaseName string
	if err := database.QueryRowContext(ctx, "select current_database()").Scan(&databaseName); err != nil {
		t.Fatalf("read test database name: %v", err)
	}
	if !strings.HasSuffix(databaseName, "_test") {
		t.Fatalf("refusing to reset database %q: test database name must end in _test", databaseName)
	}

	resetSchema(t, ctx, database)
	defer resetSchema(t, ctx, database)
	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("configure migrations: %v", err)
	}
	if err := goose.UpToContext(ctx, database, ".", 1); err != nil {
		t.Fatalf("apply foundation migration: %v", err)
	}

	id := uuid.MustParse("019cdd4c-20ec-7d18-b967-8f25172fb776")
	if _, err := database.ExecContext(ctx, "insert into users (id, email, locale) values ($1, $2, $3)", id, "owner@example.test", "es"); err != nil {
		t.Fatalf("insert foundation user: %v", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		t.Fatalf("apply identity migration: %v", err)
	}
	if err := Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}

	var email, locale, name string
	if err := database.QueryRowContext(ctx, "select email, locale, name from users where id = $1", id).Scan(&email, &locale, &name); err != nil {
		t.Fatalf("load migrated user: %v", err)
	}
	if email != "owner@example.test" || locale != "es" || name != "owner" {
		t.Fatalf("migrated user = email %q, locale %q, name %q", email, locale, name)
	}

	for _, table := range []string{"auth_sessions", "auth_accounts", "auth_verifications", "auth_passkeys", "auth_jwks"} {
		var exists bool
		if err := database.QueryRowContext(ctx, "select to_regclass($1) is not null", table).Scan(&exists); err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	var legacyTableExists bool
	if err := database.QueryRowContext(ctx, `select to_regclass('"user"') is not null`).Scan(&legacyTableExists); err != nil {
		t.Fatalf("check legacy user table: %v", err)
	}
	if legacyTableExists {
		t.Fatal("legacy Better Auth user table unexpectedly exists")
	}
	if _, err := database.ExecContext(ctx, "insert into users (email, name) values ($1, $2)", "second@example.test", "Second"); err == nil {
		t.Fatal("single-user database constraint accepted a second profile")
	}
}

func resetSchema(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	if _, err := database.ExecContext(ctx, "drop schema if exists public cascade; create schema public"); err != nil {
		t.Fatalf("reset test schema: %v", err)
	}
}
