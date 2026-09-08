package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/backups"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/config"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	databaseURL, err := config.LoadDatabaseURL()
	if err != nil || databaseURL == "" {
		logger.Error("backup configuration failed", "event", "backup.config_invalid")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		logger.Error("backup database unavailable", "event", "backup.database_unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	runner := backups.OSCommandRunner{}
	stager, err := backups.NewStager(backups.StagingConfig{
		Root: env("MAILFLOW_BACKUP_STAGING_ROOT", "/backup/source"), CDNRoot: env("MAILFLOW_CDN_ROOT", "/data/cdn"),
		MasterKeyFile: env("MAILFLOW_MASTER_KEY_FILE", "/run/secrets/master_key"), DatabaseURL: databaseURL,
		MaxFiles: envInt("MAILFLOW_BACKUP_MAX_FILES", 1_000_000), MaxBytes: envInt("MAILFLOW_BACKUP_MAX_BYTES", 100<<30),
	}, runner)
	if err != nil {
		logger.Error("backup staging configuration failed", "event", "backup.config_invalid")
		os.Exit(1)
	}
	repository := backups.NewRepository(pool)
	service, err := backups.NewService(repository, stager, runner, backups.ServiceConfig{
		Repository: env("RESTIC_REPOSITORY", "/repository"), RepositoryPasswordFile: env("RESTIC_PASSWORD_FILE", "/run/secrets/restic_password"),
		AWSAccessKeyIDFile: os.Getenv("AWS_ACCESS_KEY_ID_FILE"), AWSSecretAccessKeyFile: os.Getenv("AWS_SECRET_ACCESS_KEY_FILE"),
		RestoreDatabaseURL: databaseURL, RestoreCDNRoot: env("MAILFLOW_CDN_ROOT", "/data/cdn"),
		RestoreMasterKeyOutput: env("MAILFLOW_RESTORE_MASTER_KEY_OUTPUT", "/restore/master_key"),
	})
	if err != nil {
		logger.Error("backup service configuration failed", "event", "backup.config_invalid")
		os.Exit(1)
	}
	metricService := metrics.NewService(metrics.NewRegistry(), pool, "backup")
	service.SetMetrics(metricService.Registry())
	metricContext, cancelMetrics := context.WithCancel(ctx)
	metricsDone := make(chan struct{})
	go func() {
		defer close(metricsDone)
		_ = metricService.Run(metricContext)
	}()
	defer func() {
		cancelMetrics()
		<-metricsDone
	}()
	mode := "daemon"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	switch mode {
	case "daemon":
		schedule, parseErr := backups.ParseDailySchedule(env("MAILFLOW_BACKUP_SCHEDULE", "03:00"), env("MAILFLOW_BACKUP_TIMEZONE", "UTC"))
		if parseErr != nil {
			logger.Error("backup schedule invalid", "event", "backup.config_invalid")
			os.Exit(1)
		}
		scheduler, _ := backups.NewScheduler(repository, service, schedule, envBool("MAILFLOW_BACKUP_ENABLED", true))
		scheduler.SetReporter(func(error) {
			logger.Error("scheduled backup failed", "event", "backup.failed")
		})
		logger.Info("backup scheduler started", "event", "backup.scheduler_started", "schedule", schedule.String(), "timezone", schedule.Timezone())
		if err := scheduler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("backup scheduler stopped", "event", "backup.scheduler_stopped")
			flushMetrics(metricService)
			os.Exit(1)
		}
	case "run":
		run, runErr := service.RunOnce(ctx, "command", time.Now().UTC())
		if runErr != nil {
			logger.Error("backup failed", "event", "backup.failed")
			flushMetrics(metricService)
			os.Exit(1)
		}
		logger.Info("backup completed", "event", "backup.completed", "files", run.FileCount, "bytes", run.ByteCount)
	case "restore":
		if len(os.Args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: backup restore SNAPSHOT TARGET")
			os.Exit(2)
		}
		manifest, restoreErr := service.Restore(ctx, os.Args[2], os.Args[3], envBool("MAILFLOW_RESTORE_APPLY", false))
		if restoreErr != nil {
			logger.Error("backup restore failed", "event", "backup.restore_failed")
			flushMetrics(metricService)
			os.Exit(1)
		}
		logger.Info("backup restore verified", "event", "backup.restore_verified", "files", manifest.TotalFiles, "bytes", manifest.TotalBytes)
	default:
		fmt.Fprintln(os.Stderr, "usage: backup [daemon|run|restore SNAPSHOT TARGET]")
		os.Exit(2)
	}
}

func flushMetrics(service *metrics.Service) {
	flushContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = service.Flush(flushContext, time.Now().UTC())
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(os.Getenv(name), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}
