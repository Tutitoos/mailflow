package main

import (
	"context"
	"errors"
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
	"github.com/Tutitoos/mailflow/services/api/internal/modules/logs"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
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
	logPipeline, err := logs.NewPipeline(os.Stdout, "api", "runtime")
	if err != nil {
		panic(err)
	}
	logger := slog.New(logPipeline)
	slog.SetDefault(logger)
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
	var metricsDone chan struct{}
	var metricsCancel context.CancelFunc
	var logsDone chan struct{}
	var logsCancel context.CancelFunc
	var sentryDone chan struct{}
	var sentryCancel context.CancelFunc
	googleConfig := googleoauth.Config{ClientID: runtimeConfig.GoogleOAuthClientID, ClientSecret: runtimeConfig.GoogleOAuthClientSecret, RedirectURL: runtimeConfig.GoogleOAuthRedirectURL}
	googleClient := googleoauth.NewClient(googleConfig, nil)
	var gmailResolver *mailflowsync.GmailAccountResolver
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
		logStore, err := logs.NewStore(pool)
		if err != nil {
			logger.Error("log persistence unavailable", "event", "logs.persistence_unavailable")
			os.Exit(1)
		}
		logPipeline.Attach(logStore)
		options.Logs = logPipeline
		logsDone = make(chan struct{})
		logsContext, cancelLogs := context.WithCancel(shutdown)
		logsCancel = cancelLogs
		go func() { defer close(logsDone); _ = logPipeline.Run(logsContext) }()
		metricService := metrics.NewService(metrics.NewRegistry(), pool, "api")
		options.Metrics = metricService
		metricsDone = make(chan struct{})
		metricsContext, cancelMetrics := context.WithCancel(shutdown)
		metricsCancel = cancelMetrics
		go func() {
			defer close(metricsDone)
			if metricErr := metricService.Run(metricsContext, func(metricErr error) {
				logger.Error("metric persistence failed", "event", "metrics.persistence_failed", "error", metricErr)
			}); metricErr != nil && !errors.Is(metricErr, context.Canceled) {
				logger.Error("metric persistence stopped", "event", "metrics.persistence_stopped", "error", metricErr)
			}
		}()
		accountService = accounts.NewService(accounts.NewRepository(queries, vault))
		normalizer, normalizerErr := mail.NewNormalizer(mail.DefaultMIMEPolicy())
		if normalizerErr != nil {
			logger.Error("mail content configuration failed", "event", "mail.content_unavailable")
			os.Exit(1)
		}
		gmailResolver, err = mailflowsync.NewGmailAccountResolver(accountService, googleClient, nil, normalizer)
		if err != nil {
			logger.Error("Gmail provider configuration failed", "event", "mail.provider_unavailable")
			os.Exit(1)
		}
		cdnStore, err := cdn.NewStore(runtimeConfig.CDNRoot, runtimeConfig.CDNMaxBytes)
		if err != nil {
			logger.Error("CDN storage configuration failed", "event", "cdn.storage_unavailable", "error", err)
			os.Exit(1)
		}
		sentryService, err := mailflowsentry.NewPersistentService(pool, cdnStore, mailflowsentry.DefaultConfig())
		if err != nil {
			logger.Error("Sentry ingestion configuration failed", "event", "sentry.ingestion_unavailable")
			os.Exit(1)
		}
		sentryProjects, err := mailflowsentry.DerivedProjects(runtimeConfig.MasterKey)
		configureContext, cancelConfigure := context.WithTimeout(context.Background(), 5*time.Second)
		if err == nil {
			err = sentryService.ConfigureProjects(configureContext, sentryProjects, time.Now().UTC())
		}
		cancelConfigure()
		if err != nil {
			logger.Error("Sentry project configuration failed", "event", "sentry.projects_unavailable")
			os.Exit(1)
		}
		options.Sentry = sentryService
		sentryDone = make(chan struct{})
		sentryContext, cancelSentry := context.WithCancel(shutdown)
		sentryCancel = cancelSentry
		go func() { defer close(sentryDone); _ = sentryService.Run(sentryContext) }()
		cdnService, err := cdn.NewService(cdnStore, queries, cdn.DefaultRetention, gmailAttachmentProviderResolver{gmailResolver})
		if err != nil {
			logger.Error("CDN service configuration failed", "event", "cdn.service_unavailable", "error", err)
			os.Exit(1)
		}
		options.Readiness = pool.Ping
		options.Attachments = cdnService
		options.CurrentUsers = authbridge.NewRepository(queries)
		options.Inbox = mail.NewThreadRepository(pool)
		options.Threads = options.Inbox
		options.Search = options.Inbox
		options.ActionState = options.Inbox
		options.Mailboxes = mail.NewMailboxLabelRepository(queries)
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
		if databasePool != nil {
			logPipeline.SetStream(func(streamContext context.Context, entry logs.Entry) error {
				var ownerID string
				if err := databasePool.QueryRow(streamContext, `select id::text from users order by created_at limit 1`).Scan(&ownerID); err != nil {
					return err
				}
				_, err := store.Publish(streamContext, ownerID, "admin.log", logs.StreamPayload(entry))
				return err
			})
		}
		if databasePool != nil {
			options.Actions = mail.NewPendingActionService(mail.NewPendingActionRepository(databasePool), store)
		}
	}
	if redisClient != nil && accountService != nil {
		options.GoogleOAuth = googleoauth.NewService(googleConfig, googleoauth.NewRedisStateStore(redisClient, "mailflow"), googleClient, accountService)
		var resolverErr error
		options.Delivery, resolverErr = mail.NewDeliveryService(databasePool, mail.NewDraftRepository(databasePool), gmailOutgoingProviderResolver{gmailResolver}, options.Events, options.Attachments)
		if resolverErr != nil {
			logger.Error("mail delivery configuration failed", "event", "mail.delivery_unavailable")
			os.Exit(1)
		}
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
	listenErr := platformapp.Build(version, options).Listen(runtimeConfig.Address, fiber.ListenConfig{GracefulContext: shutdown, ShutdownTimeout: 15 * time.Second})
	if metricsCancel != nil {
		metricsCancel()
	}
	if logsCancel != nil {
		logsCancel()
	}
	if sentryCancel != nil {
		sentryCancel()
	}
	if metricsDone != nil {
		<-metricsDone
	}
	if logsDone != nil {
		<-logsDone
	}
	if sentryDone != nil {
		<-sentryDone
	}
	if listenErr != nil {
		logger.Error("api stopped", "event", "api.stopped", "error", listenErr)
		os.Exit(1)
	}
}

type gmailOutgoingProviderResolver struct {
	resolver *mailflowsync.GmailAccountResolver
}

func (resolver gmailOutgoingProviderResolver) ResolveOutgoingProvider(ctx context.Context, userID, accountID string) (mail.OutgoingProvider, error) {
	return resolver.resolver.ResolveGmail(ctx, userID, accountID)
}

type gmailAttachmentProviderResolver struct {
	resolver *mailflowsync.GmailAccountResolver
}

func (resolver gmailAttachmentProviderResolver) ResolveAttachmentProvider(ctx context.Context, userID, accountID string) (cdn.AttachmentProvider, error) {
	return resolver.resolver.ResolveGmail(ctx, userID, accountID)
}
