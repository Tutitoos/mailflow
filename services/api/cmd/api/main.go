package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	platformapp "github.com/Tutitoos/mailflow/services/api/internal/platform/app"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/config"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/privileges"
	getsentry "github.com/getsentry/sentry-go"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) == 2 && os.Args[1] == "--healthcheck" {
		if err := privileges.Drop(); err != nil {
			logger.Error("privilege drop failed", "event", "security.privilege_drop_failed", "error", err)
			os.Exit(1)
		}
		response, err := http.Get("http://127.0.0.1:8080/health/live")
		if err != nil || response.StatusCode != http.StatusOK {
			fmt.Fprintln(os.Stderr, "API healthcheck failed")
			os.Exit(1)
		}
		_ = response.Body.Close()
		return
	}
	runtimeConfig, err := config.Load()
	if err != nil {
		logger.Error("configuration failed", "event", "config.invalid", "error", err)
		os.Exit(1)
	}
	if err := privileges.Drop(); err != nil {
		logger.Error("privilege drop failed", "event", "security.privilege_drop_failed", "error", err)
		os.Exit(1)
	}
	var options platformapp.Options
	options.AuthJWKSURL = runtimeConfig.AuthJWKSURL
	if runtimeConfig.DatabaseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := database.Migrate(ctx, runtimeConfig.DatabaseURL); err != nil {
			cancel()
			logger.Error("database migration failed", "event", "database.migration_failed", "error", err)
			os.Exit(1)
		}
		pool, err := database.Open(ctx, runtimeConfig.DatabaseURL)
		cancel()
		if err != nil {
			logger.Error("database connection failed", "event", "database.unavailable", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
		options.Readiness = pool.Ping
	}
	sentryEnabled := os.Getenv("SENTRY_DSN") != ""
	if sentryEnabled {
		if err := getsentry.Init(getsentry.ClientOptions{Dsn: os.Getenv("SENTRY_DSN"), Release: version, ServerName: "api"}); err != nil {
			logger.Error("sentry initialization failed", "event", "sentry.init_failed", "error", err)
			os.Exit(1)
		}
		defer getsentry.Flush(2 * time.Second)
	}
	options.SentryEnabled = sentryEnabled
	logger.Info("api starting", "event", "api.started", "address", runtimeConfig.Address, "version", version)
	if err := platformapp.Build(version, options).Listen(runtimeConfig.Address); err != nil {
		logger.Error("api stopped", "event", "api.stopped", "error", err)
		os.Exit(1)
	}
}
