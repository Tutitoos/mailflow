package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	AuthAudience string
	AuthIssuer   string
	AuthJWKSURL  string
	Address      string
	DatabaseURL  string
}

func Load() (Config, error) {
	config := Config{
		AuthAudience: valueOrDefault("MAILFLOW_AUTH_AUDIENCE", "mailflow-api"),
		AuthIssuer:   os.Getenv("MAILFLOW_AUTH_ISSUER"),
		Address:      valueOrDefault("MAILFLOW_API_ADDRESS", ":8080"),
		AuthJWKSURL:  os.Getenv("AUTH_JWKS_URL"),
	}
	if config.AuthJWKSURL != "" && config.AuthIssuer == "" {
		return Config{}, fmt.Errorf("MAILFLOW_AUTH_ISSUER is required when AUTH_JWKS_URL is configured")
	}
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		config.DatabaseURL = databaseURL
		return config, nil
	}
	passwordFile := os.Getenv("POSTGRES_PASSWORD_FILE")
	if passwordFile == "" {
		return config, nil
	}
	passwordBytes, err := os.ReadFile(passwordFile)
	if err != nil {
		return Config{}, fmt.Errorf("read PostgreSQL password: %w", err)
	}
	user := valueOrDefault("POSTGRES_USER", "mailflow")
	database := valueOrDefault("POSTGRES_DB", "mailflow")
	host := valueOrDefault("POSTGRES_HOST", "postgres")
	port := valueOrDefault("POSTGRES_PORT", "5432")
	config.DatabaseURL = (&url.URL{
		Scheme: "postgres", User: url.UserPassword(user, strings.TrimSpace(string(passwordBytes))),
		Host: host + ":" + port, Path: database,
	}).String()
	return config, nil
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
