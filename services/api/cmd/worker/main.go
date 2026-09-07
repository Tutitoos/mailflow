package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/privileges"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := privileges.Drop(); err != nil {
		logger.Error("privilege drop failed", "event", "security.privilege_drop_failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("worker ready", "event", "worker.ready")
	<-ctx.Done()
	logger.Info("worker stopped", "event", "worker.stopped")
}
