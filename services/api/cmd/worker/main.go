package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/config"
	platformcrypto "github.com/Tutitoos/mailflow/services/api/internal/platform/crypto"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/privileges"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	redis "github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	runtimeConfig, err := config.Load()
	if err != nil {
		logger.Error("worker configuration failed", "event", "worker.config_invalid", "error", err)
		os.Exit(1)
	}
	if err := privileges.Drop(); err != nil {
		logger.Error("privilege drop failed", "event", "security.privilege_drop_failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	client := redis.NewClient(&redis.Options{Addr: valueOrDefault("REDIS_ADDRESS", "redis:6379")})
	defer client.Close()
	consumer, err := consumerName()
	if err != nil {
		logger.Error("worker configuration failed", "event", "worker.config_invalid", "error", err)
		os.Exit(1)
	}
	queueConfig := queue.DefaultConfig()
	queueConfig.Consumer = consumer
	queueConfig.Prefix = valueOrDefault("MAILFLOW_QUEUE_PREFIX", queueConfig.Prefix)
	queueConfig.ClaimTimeout, err = durationFromEnv("MAILFLOW_QUEUE_CLAIM_TIMEOUT", queueConfig.ClaimTimeout)
	if err != nil {
		logger.Error("worker configuration failed", "event", "worker.config_invalid", "error", err)
		os.Exit(1)
	}
	handleTimeout, err := durationFromEnv("MAILFLOW_QUEUE_HANDLE_TIMEOUT", 5*time.Minute)
	if err != nil {
		logger.Error("worker configuration failed", "event", "worker.config_invalid", "error", err)
		os.Exit(1)
	}
	shutdownGrace, err := durationFromEnv("MAILFLOW_QUEUE_SHUTDOWN_GRACE", 15*time.Second)
	if err != nil {
		logger.Error("worker configuration failed", "event", "worker.config_invalid", "error", err)
		os.Exit(1)
	}
	if queueConfig.ClaimTimeout <= handleTimeout {
		logger.Error("worker configuration failed", "event", "worker.config_invalid", "error", "MAILFLOW_QUEUE_CLAIM_TIMEOUT must exceed MAILFLOW_QUEUE_HANDLE_TIMEOUT")
		os.Exit(1)
	}
	store, err := queue.NewRedisStore(ctx, client, queueConfig)
	if err == nil {
		err = store.Ping(ctx)
	}
	if err != nil {
		logger.Error("Redis queue unavailable", "event", "queue.unavailable", "error", err)
		os.Exit(1)
	}
	registry := metrics.NewRegistry()
	handlers := make(map[string]queue.Handler)
	var cleanupDone chan struct{}
	var schedulerDone chan struct{}
	if runtimeConfig.DatabaseURL != "" {
		pool, err := database.Open(ctx, runtimeConfig.DatabaseURL)
		if err != nil {
			logger.Error("CDN cleanup database unavailable", "event", "cdn.cleanup_unavailable", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
		vault, err := platformcrypto.NewVault(runtimeConfig.MasterKey)
		if err != nil {
			logger.Error("account vault configuration failed", "event", "worker.config_invalid", "error", err)
			os.Exit(1)
		}
		queries := dbgen.New(pool)
		accountService := accounts.NewService(accounts.NewRepository(queries, vault))
		normalizer, err := mail.NewNormalizer(mail.DefaultMIMEPolicy())
		if err != nil {
			logger.Error("mail normalizer configuration failed", "event", "worker.config_invalid", "error", err)
			os.Exit(1)
		}
		googleConfig := googleoauth.Config{ClientID: runtimeConfig.GoogleOAuthClientID, ClientSecret: runtimeConfig.GoogleOAuthClientSecret, RedirectURL: runtimeConfig.GoogleOAuthRedirectURL}
		resolver, err := mailflowsync.NewGmailAccountResolver(accountService, googleoauth.NewClient(googleConfig, nil), nil, normalizer)
		if err != nil {
			logger.Error("Gmail resolver configuration failed", "event", "sync.unavailable", "error", err)
			os.Exit(1)
		}
		executor, err := mailflowsync.NewGmailExecutor(resolver, mail.NewRemotePageWriter())
		if err != nil {
			logger.Error("Gmail executor configuration failed", "event", "sync.unavailable", "error", err)
			os.Exit(1)
		}
		leases, err := mailflowsync.NewLeaseManager(client, queueConfig.Prefix, handleTimeout+time.Minute)
		if err != nil {
			logger.Error("sync lease configuration failed", "event", "sync.unavailable", "error", err)
			os.Exit(1)
		}
		eventStore, err := events.NewStore(ctx, client, events.DefaultConfig())
		if err != nil {
			logger.Error("sync event store configuration failed", "event", "sync.unavailable", "error", err)
			os.Exit(1)
		}
		orchestrator, err := mailflowsync.NewOrchestrator(mailflowsync.NewRunRepository(pool), store, leases, executor, eventStore, registry)
		if err != nil {
			logger.Error("sync orchestrator configuration failed", "event", "sync.unavailable", "error", err)
			os.Exit(1)
		}
		orchestrator.SetActivityTracker(mailflowsync.NewRedisActivityTracker(client, queueConfig.Prefix, mailflowsync.DefaultActivityTTL))
		handlers[mailflowsync.SyncExecuteJobKind] = orchestrator.Handler()
		schedulerDone = make(chan struct{})
		go func() {
			defer close(schedulerDone)
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				if _, scheduleErr := orchestrator.EnqueueDue(ctx); scheduleErr != nil && !errors.Is(scheduleErr, context.Canceled) {
					logger.Error("sync scheduling failed", "event", "sync.schedule_failed", "error", scheduleErr)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		cdnStore, err := cdn.NewStore(runtimeConfig.CDNRoot, runtimeConfig.CDNMaxBytes)
		if err != nil {
			logger.Error("CDN cleanup storage unavailable", "event", "cdn.cleanup_unavailable", "error", err)
			os.Exit(1)
		}
		cdnService, err := cdn.NewService(cdnStore, queries, cdn.DefaultRetention)
		if err != nil {
			logger.Error("CDN cleanup service unavailable", "event", "cdn.cleanup_unavailable", "error", err)
			os.Exit(1)
		}
		cleanupInterval, err := durationFromEnv("MAILFLOW_CDN_CLEANUP_INTERVAL", 24*time.Hour)
		if err != nil {
			logger.Error("worker configuration failed", "event", "worker.config_invalid", "error", err)
			os.Exit(1)
		}
		cleanupDone = make(chan struct{})
		go func() {
			defer close(cleanupDone)
			_ = cdnService.RunCleanupLoop(ctx, cleanupInterval, func(result cdn.CleanupResult, cleanupErr error) {
				if errors.Is(cleanupErr, context.Canceled) {
					return
				}
				if cleanupErr != nil {
					logger.Error("CDN cleanup failed", "event", "cdn.cleanup_failed", "error", cleanupErr)
					return
				}
				logger.Info("CDN cleanup completed", "event", "cdn.cleanup_completed", "expired", result.Expired, "orphans", result.Orphans)
			})
		}()
	}
	observer := queue.ObserverFunc(func(event queue.Event) {
		if err := registry.Add("mailflow_queue_jobs_total", 1, map[string]string{"operation": event.Operation, "result": event.Result, "service": "worker"}); err != nil {
			logger.Error("queue metric rejected", "event", "metrics.rejected", "error", err)
		}
	})
	runner := queue.NewRunner(store, handlers, observer, queue.RunnerConfig{HandleTimeout: handleTimeout, ShutdownGrace: shutdownGrace})
	logger.Info("worker ready", "event", "worker.ready", "consumer", consumer)
	runErr := runner.Run(ctx)
	stop()
	if cleanupDone != nil {
		<-cleanupDone
	}
	if schedulerDone != nil {
		<-schedulerDone
	}
	if runErr != nil {
		logger.Error("worker failed", "event", "worker.failed", "error", runErr)
		os.Exit(1)
	}
	logger.Info("worker stopped", "event", "worker.stopped")
}

func consumerName() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	return hostname + "-" + strconv.Itoa(os.Getpid()), nil
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, errors.New(name + " must be a positive duration")
	}
	return duration, nil
}
