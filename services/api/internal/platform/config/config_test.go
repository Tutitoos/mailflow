package config

import "testing"

func TestAuthenticationConfiguration(t *testing.T) {
	t.Setenv("AUTH_JWKS_URL", "https://mailflow.test/api/auth/jwks")
	t.Setenv("MAILFLOW_AUTH_ISSUER", "")
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
