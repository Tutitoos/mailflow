package httpapi

import (
	"errors"
	"strings"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/gofiber/fiber/v3"
)

func synchronizeAccount(syncer SyncRequester) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		idempotencyKey := strings.TrimSpace(c.Get("Idempotency-Key"))
		if len(idempotencyKey) < 16 || len(idempotencyKey) > 128 {
			return newProblem(fiber.StatusBadRequest, "idempotency_key_required", "Idempotency key required", "Provide a valid Idempotency-Key header.")
		}
		if syncer == nil {
			return newProblem(fiber.StatusServiceUnavailable, "sync_unavailable", "Synchronization unavailable", "Synchronization is not configured on this installation.")
		}
		run, err := syncer.Request(c.Context(), user.ID, c.Params("accountId"))
		if errors.Is(err, mailflowsync.ErrRunExists) {
			return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "already_queued"})
		}
		if errors.Is(err, mailflowsync.ErrInvalidRun) || errors.Is(err, mailflowsync.ErrRunNotFound) {
			return newProblem(fiber.StatusNotFound, "account_not_found", "Account not found", "The requested account does not exist.")
		}
		if err != nil {
			return newProblem(fiber.StatusServiceUnavailable, "sync_enqueue_failed", "Synchronization unavailable", "The synchronization request could not be queued.")
		}
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"status": "queued", "runId": run.ID})
	}
}
