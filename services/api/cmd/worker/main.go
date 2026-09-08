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
	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/logs"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
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
	logPipeline, err := logs.NewPipeline(os.Stdout, "worker", "runtime")
	if err != nil {
		panic(err)
	}
	logger := slog.New(logPipeline)
	slog.SetDefault(logger)
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
	heartbeats, err := admin.NewHeartbeats(client, queueConfig.Prefix)
	if err != nil {
		logger.Error("worker heartbeat unavailable", "event", "worker.heartbeat_unavailable")
		os.Exit(1)
	}
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		if heartbeatErr := heartbeats.Run(ctx, "worker"); heartbeatErr != nil && !errors.Is(heartbeatErr, context.Canceled) {
			logger.Error("worker heartbeat stopped", "event", "worker.heartbeat_stopped")
		}
	}()
	registry := metrics.NewRegistry()
	handlers := make(map[string]queue.Handler)
	var cleanupDone chan struct{}
	var schedulerDone chan struct{}
	var actionDone chan struct{}
	var metricsDone chan struct{}
	var logsDone chan struct{}
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
		logStore, err := logs.NewStore(pool)
		if err != nil {
			logger.Error("log persistence unavailable", "event", "logs.persistence_unavailable")
			os.Exit(1)
		}
		logPipeline.Attach(logStore)
		logsDone = make(chan struct{})
		go func() { defer close(logsDone); _ = logPipeline.Run(ctx) }()
		metricService := metrics.NewService(registry, pool, "worker")
		metricsDone = make(chan struct{})
		go func() {
			defer close(metricsDone)
			if metricErr := metricService.Run(ctx, func(metricErr error) {
				logger.Error("metric persistence failed", "event", "metrics.persistence_failed", "error", metricErr)
			}); metricErr != nil && !errors.Is(metricErr, context.Canceled) {
				logger.Error("metric persistence stopped", "event", "metrics.persistence_stopped", "error", metricErr)
			}
		}()
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
		logPipeline.SetStream(func(streamContext context.Context, entry logs.Entry) error {
			var ownerID string
			if err := pool.QueryRow(streamContext, `select id::text from users order by created_at limit 1`).Scan(&ownerID); err != nil {
				return err
			}
			_, err := eventStore.Publish(streamContext, ownerID, "admin.log", logs.StreamPayload(entry))
			return err
		})
		orchestrator, err := mailflowsync.NewOrchestrator(mailflowsync.NewRunRepository(pool), store, leases, executor, eventStore, registry)
		if err != nil {
			logger.Error("sync orchestrator configuration failed", "event", "sync.unavailable", "error", err)
			os.Exit(1)
		}
		orchestrator.SetActivityTracker(mailflowsync.NewRedisActivityTracker(client, queueConfig.Prefix, mailflowsync.DefaultActivityTTL))
		actionService := mail.NewPendingActionService(mail.NewPendingActionRepository(pool), eventStore)
		actionProcessor, err := mail.NewActionProcessor(actionService, gmailActionProviderResolver{resolver}, mail.NewThreadRepository(pool), mail.NewThreadRepository(pool))
		if err != nil {
			logger.Error("mail action processor configuration failed", "event", "mail.actions_unavailable", "error", err)
			os.Exit(1)
		}
		actionDone = make(chan struct{})
		go func() {
			defer close(actionDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				var userID string
				if queryErr := pool.QueryRow(ctx, `select id::text from users order by created_at limit 1`).Scan(&userID); queryErr == nil {
					for {
						processed, processErr := actionProcessor.ProcessNext(ctx, userID)
						if processErr != nil && !errors.Is(processErr, context.Canceled) {
							_ = registry.Add("mailflow_mail_actions_total", 1, map[string]string{"service": "worker", "module": "mail", "provider": "google", "operation": "apply", "result": "failure"})
							logger.Error("mail action processing failed", "event", "mail.action_failed")
						}
						if processed && processErr == nil {
							_ = registry.Add("mailflow_mail_actions_total", 1, map[string]string{"service": "worker", "module": "mail", "provider": "google", "operation": "apply", "result": "success"})
						}
						if !processed || processErr != nil {
							break
						}
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
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
		artifactProcessor, err := mailflowsentry.NewArtifactProcessor(pool, cdnStore)
		if err != nil {
			logger.Error("Sentry artifact processor unavailable", "event", "sentry.artifacts_unavailable", "error", err)
			os.Exit(1)
		}
		handlers[mailflowsentry.ArtifactProcessJobKind] = artifactProcessor.Handler()
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
					_ = registry.Add("mailflow_cdn_cleanup_total", 1, map[string]string{"service": "worker", "module": "cdn", "operation": "cleanup", "result": "failure"})
					logger.Error("CDN cleanup failed", "event", "cdn.cleanup_failed", "error", cleanupErr)
					return
				}
				_ = registry.Add("mailflow_cdn_cleanup_total", 1, map[string]string{"service": "worker", "module": "cdn", "operation": "cleanup", "result": "success"})
				_ = registry.Set("mailflow_cdn_expired_objects", string(metrics.Gauge), float64(result.Expired), map[string]string{"service": "worker", "module": "cdn"})
				_ = registry.Set("mailflow_cdn_orphan_objects", string(metrics.Gauge), float64(result.Orphans), map[string]string{"service": "worker", "module": "cdn"})
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
	if actionDone != nil {
		<-actionDone
	}
	if metricsDone != nil {
		<-metricsDone
	}
	if logsDone != nil {
		<-logsDone
	}
	<-heartbeatDone
	if runErr != nil {
		logger.Error("worker failed", "event", "worker.failed", "error", runErr)
		os.Exit(1)
	}
	logger.Info("worker stopped", "event", "worker.stopped")
}

type gmailActionProviderResolver struct {
	resolver *mailflowsync.GmailAccountResolver
}

func (resolver gmailActionProviderResolver) ResolveActionProvider(ctx context.Context, userID, accountID string) (mail.ActionProvider, error) {
	return resolver.resolver.ResolveGmail(ctx, userID, accountID)
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
