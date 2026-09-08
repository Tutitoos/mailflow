package config

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthenticationConfiguration(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://mailflow.test/api/auth/jwks")
	t.Setenv("MAILFLOW_AUTH_ISSUER", "")
	t.Setenv("MAILFLOW_MASTER_KEY_FILE", "")
	t.Setenv("POSTGRES_PASSWORD_FILE", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected issuer to be required when JWKS validation is enabled")
	}

	t.Setenv("MAILFLOW_AUTH_ISSUER", "https://mailflow.test")
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.AuthAudience != "mailflow-api" || configuration.AuthIssuer != "https://mailflow.test" {
		t.Fatalf("unexpected authentication configuration: %+v", configuration)
	}
}

func TestDatabaseURLCanLoadBeforeDisasterRecoveryKey(t *testing.T) {
	password := filepath.Join(t.TempDir(), "postgres_password")
	if err := os.WriteFile(password, []byte("database-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_PASSWORD_FILE", password)
	t.Setenv("POSTGRES_HOST", "restore-postgres")
	t.Setenv("MAILFLOW_MASTER_KEY_FILE", filepath.Join(t.TempDir(), "missing-master-key"))

	databaseURL, err := LoadDatabaseURL()
	if err != nil || !strings.Contains(databaseURL, "restore-postgres:5432/mailflow") {
		t.Fatalf("database URL=%q err=%v", databaseURL, err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("full application config accepted a missing master key")
	}
}

func TestMasterKeyConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master_key")
	want := bytes.Repeat([]byte{42}, 32)
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(want)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAILFLOW_MASTER_KEY_FILE", path)
	t.Setenv("POSTGRES_PASSWORD_FILE", "")

	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configuration.MasterKey, want) {
		t.Fatal("loaded master key does not match the secret file")
	}

	if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected an invalid master key to fail")
	}
}

func TestGoogleOAuthSecretLoadsFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "google_oauth_client_secret")
	if err := os.WriteFile(path, []byte("installation-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "installation-client")
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET_FILE", path)
	t.Setenv("GOOGLE_OAUTH_REDIRECT_URL", "https://mail.example.test/api/v1/oauth/google/callback")
	t.Setenv("POSTGRES_PASSWORD_FILE", "")
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.GoogleOAuthClientID != "installation-client" || configuration.GoogleOAuthClientSecret != "installation-secret" {
		t.Fatal("unexpected Google OAuth configuration")
	}
}
