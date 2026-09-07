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
	AuthAudience string
	AuthIssuer   string
	AuthJWKSURL  string
	Address      string
	CDNMaxBytes  int64
	CDNRoot      string
	DatabaseURL  string
	MasterKey    []byte
	RedisAddress string
}

func Load() (Config, error) {
	config := Config{
		AuthAudience: valueOrDefault("MAILFLOW_AUTH_AUDIENCE", "mailflow-api"),
		AuthIssuer:   os.Getenv("MAILFLOW_AUTH_ISSUER"),
		Address:      valueOrDefault("MAILFLOW_API_ADDRESS", ":8080"),
		AuthJWKSURL:  os.Getenv("AUTH_JWKS_URL"),
		CDNRoot:      valueOrDefault("MAILFLOW_CDN_ROOT", "/data/cdn"),
		RedisAddress: os.Getenv("REDIS_ADDRESS"),
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
