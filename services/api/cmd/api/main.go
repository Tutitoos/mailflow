package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	platformapp "github.com/Tutitoos/mailflow/services/api/internal/platform/app"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/config"
	platformcrypto "github.com/Tutitoos/mailflow/services/api/internal/platform/crypto"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/privileges"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	getsentry "github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
	redis "github.com/redis/go-redis/v9"
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
	if len(os.Args) == 2 && os.Args[1] == "--migrate" {
		if runtimeConfig.DatabaseURL == "" {
			logger.Error("database migration failed", "event", "database.migration_failed", "error", "database URL is required")
			os.Exit(1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := database.Migrate(ctx, runtimeConfig.DatabaseURL); err != nil {
			logger.Error("database migration failed", "event", "database.migration_failed", "error", err)
			os.Exit(1)
		}
		logger.Info("database migrations applied", "event", "database.migrations_applied")
		return
	}
	var options platformapp.Options
	shutdown, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	options.Shutdown = shutdown
	options.AuthAudience = runtimeConfig.AuthAudience
	options.AuthIssuer = runtimeConfig.AuthIssuer
	options.AuthJWKSURL = runtimeConfig.AuthJWKSURL
	var accountService *accounts.Service
	var databasePool *pgxpool.Pool
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
		databasePool = pool
		vault, err := platformcrypto.NewVault(runtimeConfig.MasterKey)
		if err != nil {
			logger.Error("account vault configuration failed", "event", "config.invalid", "error", err)
			os.Exit(1)
		}
		queries := dbgen.New(pool)
		cdnStore, err := cdn.NewStore(runtimeConfig.CDNRoot, runtimeConfig.CDNMaxBytes)
		if err != nil {
			logger.Error("CDN storage configuration failed", "event", "cdn.storage_unavailable", "error", err)
			os.Exit(1)
		}
		cdnService, err := cdn.NewService(cdnStore, queries, cdn.DefaultRetention)
		if err != nil {
			logger.Error("CDN service configuration failed", "event", "cdn.service_unavailable", "error", err)
			os.Exit(1)
		}
		options.Readiness = pool.Ping
		options.Attachments = cdnService
		options.CurrentUsers = authbridge.NewRepository(queries)
		options.Inbox = mail.NewThreadRepository(pool)
		options.Mailboxes = mail.NewMailboxLabelRepository(queries)
		accountService = accounts.NewService(accounts.NewRepository(queries, vault))
		options.Accounts = accountService
	}
	var redisClient *redis.Client
	if runtimeConfig.RedisAddress != "" {
		client := redis.NewClient(&redis.Options{Addr: runtimeConfig.RedisAddress})
		redisClient = client
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		store, err := events.NewStore(ctx, client, events.DefaultConfig())
		cancel()
		if err != nil {
			_ = client.Close()
			logger.Error("event store connection failed", "event", "events.unavailable", "error", err)
			os.Exit(1)
		}
		defer client.Close()
		options.Events = store
	}
	googleConfig := googleoauth.Config{ClientID: runtimeConfig.GoogleOAuthClientID, ClientSecret: runtimeConfig.GoogleOAuthClientSecret, RedirectURL: runtimeConfig.GoogleOAuthRedirectURL}
	if redisClient != nil && accountService != nil {
		options.GoogleOAuth = googleoauth.NewService(googleConfig, googleoauth.NewRedisStateStore(redisClient, "mailflow"), googleoauth.NewClient(googleConfig, nil), accountService)
		queueConfig := queue.DefaultConfig()
		if prefix := os.Getenv("MAILFLOW_QUEUE_PREFIX"); prefix != "" {
			queueConfig.Prefix = prefix
		}
		queueStore, queueErr := queue.NewRedisStore(context.Background(), redisClient, queueConfig)
		if queueErr != nil {
			logger.Error("sync queue configuration failed", "event", "sync.unavailable", "error", queueErr)
			os.Exit(1)
		}
		options.Sync, queueErr = mailflowsync.NewScheduler(mailflowsync.NewRunRepository(databasePool), queueStore)
		if queueErr != nil {
			logger.Error("sync scheduler configuration failed", "event", "sync.unavailable", "error", queueErr)
			os.Exit(1)
		}
		options.Sync.SetActivityTracker(mailflowsync.NewRedisActivityTracker(redisClient, queueConfig.Prefix, mailflowsync.DefaultActivityTTL))
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
	if err := platformapp.Build(version, options).Listen(runtimeConfig.Address, fiber.ListenConfig{GracefulContext: shutdown, ShutdownTimeout: 15 * time.Second}); err != nil {
		logger.Error("api stopped", "event", "api.stopped", "error", err)
		os.Exit(1)
	}
}
