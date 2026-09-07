package httpapi

import (
	"errors"
	"strconv"

	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/gofiber/fiber/v3"
)

func adminSentryIssues(service *mailflowsentry.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if service == nil {
			return sentryAdminUnavailable()
		}
		limit := 100
		var err error
		if raw := c.Query("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil {
				return invalidSentryAdminQuery()
			}
		}
		issues, err := service.ListIssues(c.Context(), c.Query("status"), limit)
		if errors.Is(err, mailflowsentry.ErrInvalidEnvelope) {
			return invalidSentryAdminQuery()
		}
		if err != nil {
			return sentryAdminUnavailable()
		}
		return c.JSON(fiber.Map{"items": issues})
	}
}

func setAdminSentryIssueStatus(service *mailflowsentry.Service) fiber.Handler {
	type requestBody struct {
		Status string `json:"status"`
	}
	return func(c fiber.Ctx) error {
		if service == nil {
			return sentryAdminUnavailable()
		}
		var request requestBody
		if c.Bind().Body(&request) != nil {
			return invalidSentryAdminQuery()
		}
		issue, err := service.SetIssueStatus(c.Context(), c.Params("issueId"), request.Status)
		if errors.Is(err, mailflowsentry.ErrInvalidEnvelope) {
			return invalidSentryAdminQuery()
		}
		if errors.Is(err, mailflowsentry.ErrIssueNotFound) {
			return newProblem(fiber.StatusNotFound, "sentry_issue_not_found", "Sentry issue not found", "The requested issue does not exist.")
		}
		if err != nil {
			return sentryAdminUnavailable()
		}
		return c.JSON(issue)
	}
}

func invalidSentryAdminQuery() error {
	return newProblem(fiber.StatusBadRequest, "invalid_sentry_query", "Invalid Sentry query", "The requested issue filter, limit, identifier, or status is invalid.")
}

func sentryAdminUnavailable() error {
	return newProblem(fiber.StatusServiceUnavailable, "sentry_admin_unavailable", "Sentry issues unavailable", "Sentry issue data is temporarily unavailable.")
}
