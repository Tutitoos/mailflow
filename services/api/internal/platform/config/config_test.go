package config

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
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
