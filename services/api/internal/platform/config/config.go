package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AlertSMTPHost              string
	AlertSMTPPort              int
	AlertSMTPUsername          string
	AlertSMTPPassword          string
	AlertSMTPFrom              string
	AlertSMTPTo                string
	AlertSMTPImplicitTLS       bool
	AlertFallbackEnabled       bool
	AuthAudience               string
	AuthIssuer                 string
	AuthJWKSURL                string
	Address                    string
	CDNMaxBytes                int64
	CDNRoot                    string
	DatabaseURL                string
	MasterKey                  []byte
	RedisAddress               string
	GoogleOAuthClientID        string
	GoogleOAuthClientSecret    string
	GoogleOAuthRedirectURL     string
	MicrosoftOAuthClientID     string
	MicrosoftOAuthClientSecret string
	MicrosoftOAuthRedirectURL  string
	MicrosoftOAuthAuthority    string
}

func Load() (Config, error) {
	config := Config{
		AlertSMTPHost:             os.Getenv("MAILFLOW_ALERT_SMTP_HOST"),
		AlertSMTPUsername:         os.Getenv("MAILFLOW_ALERT_SMTP_USERNAME"),
		AlertSMTPFrom:             os.Getenv("MAILFLOW_ALERT_SMTP_FROM"),
		AlertSMTPTo:               os.Getenv("MAILFLOW_ALERT_SMTP_TO"),
		AlertSMTPImplicitTLS:      strings.EqualFold(os.Getenv("MAILFLOW_ALERT_SMTP_IMPLICIT_TLS"), "true"),
		AlertFallbackEnabled:      strings.EqualFold(os.Getenv("MAILFLOW_ALERT_CONNECTED_ACCOUNT_FALLBACK"), "true"),
		AuthAudience:              valueOrDefault("MAILFLOW_AUTH_AUDIENCE", "mailflow-api"),
		AuthIssuer:                os.Getenv("MAILFLOW_AUTH_ISSUER"),
		Address:                   valueOrDefault("MAILFLOW_API_ADDRESS", ":8080"),
		AuthJWKSURL:               os.Getenv("AUTH_JWKS_URL"),
		CDNRoot:                   valueOrDefault("MAILFLOW_CDN_ROOT", "/data/cdn"),
		RedisAddress:              os.Getenv("REDIS_ADDRESS"),
		GoogleOAuthClientID:       os.Getenv("GOOGLE_OAUTH_CLIENT_ID"),
		GoogleOAuthRedirectURL:    os.Getenv("GOOGLE_OAUTH_REDIRECT_URL"),
		MicrosoftOAuthClientID:    os.Getenv("MICROSOFT_OAUTH_CLIENT_ID"),
		MicrosoftOAuthRedirectURL: os.Getenv("MICROSOFT_OAUTH_REDIRECT_URL"),
		MicrosoftOAuthAuthority:   valueOrDefault("MICROSOFT_OAUTH_AUTHORITY", "common"),
	}
	alertPort, err := strconv.Atoi(valueOrDefault("MAILFLOW_ALERT_SMTP_PORT", "587"))
	if err != nil || alertPort < 1 || alertPort > 65535 {
		return Config{}, fmt.Errorf("MAILFLOW_ALERT_SMTP_PORT must be a valid TCP port")
	}
	config.AlertSMTPPort = alertPort
	if passwordFile := os.Getenv("MAILFLOW_ALERT_SMTP_PASSWORD_FILE"); passwordFile != "" {
		password, readErr := os.ReadFile(passwordFile)
		if readErr != nil {
			return Config{}, fmt.Errorf("read alert SMTP password: %w", readErr)
		}
		config.AlertSMTPPassword = strings.TrimSpace(string(password))
	}
	if secretFile := os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET_FILE"); secretFile != "" {
		secret, err := os.ReadFile(secretFile)
		if err != nil {
			return Config{}, fmt.Errorf("read Google OAuth client secret: %w", err)
		}
		config.GoogleOAuthClientSecret = strings.TrimSpace(string(secret))
	}
	if secretFile := os.Getenv("MICROSOFT_OAUTH_CLIENT_SECRET_FILE"); secretFile != "" {
		secret, err := os.ReadFile(secretFile)
		if err != nil {
			return Config{}, fmt.Errorf("read Microsoft OAuth client secret: %w", err)
		}
		config.MicrosoftOAuthClientSecret = strings.TrimSpace(string(secret))
	}
	maxBytes, err := strconv.ParseInt(valueOrDefault("MAILFLOW_CDN_MAX_BYTES", "26214400"), 10, 64)
	if err != nil || maxBytes <= 0 {
		return Config{}, fmt.Errorf("MAILFLOW_CDN_MAX_BYTES must be a positive integer")
	}
	config.CDNMaxBytes = maxBytes
	if config.AuthJWKSURL != "" && config.AuthIssuer == "" {
		return Config{}, fmt.Errorf("MAILFLOW_AUTH_ISSUER is required when AUTH_JWKS_URL is configured")
	}
	if masterKeyFile := os.Getenv("MAILFLOW_MASTER_KEY_FILE"); masterKeyFile != "" {
		encoded, err := os.ReadFile(masterKeyFile)
		if err != nil {
			return Config{}, fmt.Errorf("read master key: %w", err)
		}
		config.MasterKey, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
		if err != nil || len(config.MasterKey) != 32 {
			return Config{}, fmt.Errorf("MAILFLOW_MASTER_KEY_FILE must contain a base64-encoded 32-byte key")
		}
	}
	databaseURL, err := LoadDatabaseURL()
	if err != nil {
		return Config{}, err
	}
	config.DatabaseURL = databaseURL
	return config, nil
}

// LoadDatabaseURL resolves only PostgreSQL configuration. Operational tools use
// it during disaster recovery, before the application master key is available.
func LoadDatabaseURL() (string, error) {
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		return databaseURL, nil
	}
	passwordFile := os.Getenv("POSTGRES_PASSWORD_FILE")
	if passwordFile == "" {
		return "", nil
	}
	passwordBytes, err := os.ReadFile(passwordFile)
	if err != nil {
		return "", fmt.Errorf("read PostgreSQL password: %w", err)
	}
	user := valueOrDefault("POSTGRES_USER", "mailflow")
	database := valueOrDefault("POSTGRES_DB", "mailflow")
	host := valueOrDefault("POSTGRES_HOST", "postgres")
	port := valueOrDefault("POSTGRES_PORT", "5432")
	return (&url.URL{
		Scheme: "postgres", User: url.UserPassword(user, strings.TrimSpace(string(passwordBytes))),
		Host: host + ":" + port, Path: database,
	}).String(), nil
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
