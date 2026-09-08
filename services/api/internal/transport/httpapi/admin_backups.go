package httpapi

import (
	"context"
	"strconv"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/backups"
	"github.com/gofiber/fiber/v3"
)

func adminBackups(repository *backups.Repository) fiber.Handler {
	return func(c fiber.Ctx) error {
		if repository == nil {
			return adminOperationsUnavailable()
		}
		limit := backups.DefaultRunLimit
		if value := c.Query("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > backups.MaxRunLimit {
				return newProblem(fiber.StatusBadRequest, "invalid_backup_query", "Invalid backup query", "The backup history limit must be between 1 and 100.")
			}
			limit = parsed
		}
		ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
		defer cancel()
		status, err := repository.Status(ctx, time.Now().UTC(), limit)
		if err != nil {
			return adminOperationsUnavailable()
		}
		return c.JSON(status)
	}
}
