package httpapi

import (
	"context"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/alerts"
	"github.com/gofiber/fiber/v3"
)

func adminAlerts(service *alerts.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if service == nil {
			return adminOperationsUnavailable()
		}
		ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
		defer cancel()
		status, err := service.Status(ctx, alerts.DefaultLimit)
		if err != nil {
			return adminOperationsUnavailable()
		}
		return c.JSON(status)
	}
}

func testAdminAlert(service *alerts.Service) fiber.Handler {
	type request struct {
		Confirmation string `json:"confirmation"`
	}
	return func(c fiber.Ctx) error {
		if service == nil {
			return adminOperationsUnavailable()
		}
		key := strings.TrimSpace(c.Get("Idempotency-Key"))
		var body request
		if len(key) < 16 || len(key) > 128 || c.Bind().Body(&body) != nil || body.Confirmation != "send" {
			return newProblem(fiber.StatusUnprocessableEntity, "invalid_alert_test", "Invalid alert test", "Confirmation and a valid Idempotency-Key are required.")
		}
		ctx, cancel := context.WithTimeout(c.Context(), 20*time.Second)
		defer cancel()
		incident, err := service.Test(ctx, key)
		if err != nil {
			return adminOperationsUnavailable()
		}
		return c.Status(fiber.StatusAccepted).JSON(incident)
	}
}
