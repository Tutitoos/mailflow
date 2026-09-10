package httpapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/gofiber/fiber/v3"
)

func requireAdminOwner(c fiber.Ctx) error {
	if _, ok := authbridge.UserFromContext(c.Context()); !ok {
		return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
	}
	return c.Next()
}

func adminStatus(service *admin.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
		defer cancel()
		return c.JSON(service.Status(ctx))
	}
}

func adminQueue(service *admin.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, _ := authbridge.UserFromContext(c.Context())
		overview, err := service.QueueOverview(c.Context(), user.ID)
		if err != nil {
			return adminOperationsUnavailable()
		}
		return c.JSON(overview)
	}
}

func retryAdminQueue(service *admin.Service, synchronizer SyncRequester) fiber.Handler {
	type requestBody struct {
		AccountID    string `json:"accountId"`
		Confirmation string `json:"confirmation"`
	}
	return func(c fiber.Ctx) error {
		user, _ := authbridge.UserFromContext(c.Context())
		var request requestBody
		idempotencyKey := strings.TrimSpace(c.Get("Idempotency-Key"))
		if c.Bind().Body(&request) != nil || request.Confirmation != "retry" || idempotencyKey == "" {
			return invalidAdminOperation()
		}
		operation, created, err := service.RetrySynchronization(c.Context(), synchronizer, user.ID, request.AccountID, idempotencyKey)
		if errors.Is(err, admin.ErrInvalidOperation) {
			return invalidAdminOperation()
		}
		if errors.Is(err, admin.ErrTargetNotFound) {
			return newProblem(fiber.StatusNotFound, "account_not_found", "Account not found", "The requested account does not exist.")
		}
		if err != nil {
			return adminOperationsUnavailable()
		}
		status := fiber.StatusAccepted
		if !created {
			status = fiber.StatusOK
		}
		return c.Status(status).JSON(fiber.Map{"operation": operation, "created": created})
	}
}

func resolveAdminDeadLetter(service *admin.Service) fiber.Handler {
	type requestBody struct {
		Confirmation string `json:"confirmation"`
	}
	return func(c fiber.Ctx) error {
		user, _ := authbridge.UserFromContext(c.Context())
		var request requestBody
		idempotencyKey := strings.TrimSpace(c.Get("Idempotency-Key"))
		if c.Bind().Body(&request) != nil || request.Confirmation != "resolve" || idempotencyKey == "" {
			return invalidAdminOperation()
		}
		operation, created, err := service.ResolveDeadLetter(c.Context(), user.ID, c.Params("receipt"), idempotencyKey)
		if errors.Is(err, admin.ErrInvalidOperation) {
			return invalidAdminOperation()
		}
		if err != nil {
			return adminOperationsUnavailable()
		}
		return c.JSON(fiber.Map{"operation": operation, "created": created})
	}
}

func adminCDNStatus(service *admin.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		status, err := service.CDNStatus(c.Context())
		if err != nil {
			return adminOperationsUnavailable()
		}
		return c.JSON(status)
	}
}

func invalidAdminOperation() error {
	return newProblem(fiber.StatusUnprocessableEntity, "invalid_admin_operation", "Invalid admin operation", "A bounded idempotency key, account, and explicit confirmation are required.")
}

func adminOperationsUnavailable() error {
	return newProblem(fiber.StatusServiceUnavailable, "admin_operations_unavailable", "Admin operations unavailable", "Operational data or controls are temporarily unavailable.")
}
