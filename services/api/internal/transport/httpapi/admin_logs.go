package httpapi

import (
	"errors"
	"strconv"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/logs"
	"github.com/gofiber/fiber/v3"
)

func adminLogs(pipeline *logs.Pipeline) fiber.Handler {
	return func(c fiber.Ctx) error {
		if pipeline == nil {
			return logsUnavailable()
		}
		now := time.Now().UTC()
		from, err := metricTime(c.Query("from"), now.Add(-time.Hour))
		if err != nil {
			return invalidLogsQuery()
		}
		until, err := metricTime(c.Query("until"), now.Add(time.Second))
		if err != nil {
			return invalidLogsQuery()
		}
		limit := logs.DefaultQueryLimit
		if raw := c.Query("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil {
				return invalidLogsQuery()
			}
		}
		items, err := pipeline.Query(c.Context(), logs.Query{
			From: from, Until: until, Service: c.Query("service"), Module: c.Query("module"),
			Level: c.Query("level"), Event: c.Query("event"), RequestID: c.Query("requestId"), Limit: limit,
		})
		if errors.Is(err, logs.ErrInvalidQuery) {
			return invalidLogsQuery()
		}
		if err != nil {
			return logsUnavailable()
		}
		return c.JSON(fiber.Map{"items": items, "dropped": pipeline.Dropped()})
	}
}

func logDebugStatus(pipeline *logs.Pipeline) fiber.Handler {
	return func(c fiber.Ctx) error {
		if pipeline == nil {
			return logsUnavailable()
		}
		until, err := pipeline.DebugUntil(c.Context(), time.Now().UTC())
		if err != nil {
			return logsUnavailable()
		}
		return c.JSON(fiber.Map{"enabled": until != nil, "enabledUntil": until})
	}
}

func setLogDebug(pipeline *logs.Pipeline) fiber.Handler {
	type requestBody struct {
		DurationSeconds int `json:"durationSeconds"`
	}
	return func(c fiber.Ctx) error {
		if pipeline == nil {
			return logsUnavailable()
		}
		var request requestBody
		if c.Bind().Body(&request) != nil || request.DurationSeconds < 0 || request.DurationSeconds > int(logs.MaxDebugDuration/time.Second) {
			return invalidLogsQuery()
		}
		until, err := pipeline.SetDebug(c.Context(), time.Now().UTC(), time.Duration(request.DurationSeconds)*time.Second)
		if errors.Is(err, logs.ErrInvalidQuery) {
			return invalidLogsQuery()
		}
		if err != nil {
			return logsUnavailable()
		}
		return c.JSON(fiber.Map{"enabled": until != nil, "enabledUntil": until})
	}
}

func invalidLogsQuery() error {
	return newProblem(fiber.StatusBadRequest, "invalid_logs_query", "Invalid logs query", "The requested filters, range, limit, or debug duration are invalid.")
}

func logsUnavailable() error {
	return newProblem(fiber.StatusServiceUnavailable, "logs_unavailable", "Logs unavailable", "Operational logs are temporarily unavailable.")
}
