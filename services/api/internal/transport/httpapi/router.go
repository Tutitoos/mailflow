package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	jwtware "github.com/gofiber/contrib/v3/jwt"
	fibersentry "github.com/gofiber/contrib/v3/sentry"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

type Dependencies struct {
	Admin         *admin.Service
	AuthJWKSURL   string
	Readiness     func(context.Context) error
	Sentry        *mailflowsentry.Service
	Translations  *translations.Catalog
	CaptureSentry bool
}

func New(deps Dependencies) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:             "Mailflow API",
		PassLocalsToContext: true,
		ReadTimeout:         15 * time.Second,
		WriteTimeout:        30 * time.Second,
		ErrorHandler:        problemHandler,
	})
	app.Use(recover.New(), requestid.New())

	app.Post("/sentry/api/1/envelope/", sentryIngest(deps.Sentry))
	app.Post("/sentry/api/1/store/", sentryIngest(deps.Sentry))
	if deps.CaptureSentry {
		app.Use(fibersentry.New(fibersentry.Config{Repanic: true, WaitForDelivery: false}))
	}

	app.Get("/health/live", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "service": "api"})
	})
	app.Get("/health/ready", func(c fiber.Ctx) error {
		if deps.Readiness != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := deps.Readiness(ctx); err != nil {
				return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "unavailable", "service": "api"})
			}
		}
		return c.JSON(fiber.Map{"status": "ready", "service": "api"})
	})

	v1 := app.Group("/api/v1")
	v1.Get("/translations/:locale", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"locale": c.Params("locale"), "messages": deps.Translations.Locale(c.Params("locale"))})
	})
	if deps.AuthJWKSURL != "" {
		v1.Use(jwtware.New(jwtware.Config{
			JWKSetURLs: []string{deps.AuthJWKSURL},
			ErrorHandler: func(_ fiber.Ctx, _ error) error {
				return fiber.NewError(fiber.StatusUnauthorized, "invalid or missing access token")
			},
		}))
	}
	adminRoutes := v1.Group("/admin")
	adminRoutes.Get("/status", func(c fiber.Ctx) error { return c.JSON(deps.Admin.Status()) })
	adminRoutes.Get("/metrics", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"items": deps.Admin.Metrics()}) })

	return app
}

func sentryIngest(service *mailflowsentry.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		receipt, err := service.Accept(c.Get("X-Sentry-Event-ID"), c.Body())
		if errors.Is(err, mailflowsentry.ErrEnvelopeTooLarge) {
			return fiber.NewError(fiber.StatusRequestEntityTooLarge, err.Error())
		}
		if err != nil {
			return err
		}
		return c.Status(fiber.StatusAccepted).JSON(receipt)
	}
}

func problemHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	title := fiber.ErrInternalServerError.Message
	if fiberErr, ok := err.(*fiber.Error); ok {
		code = fiberErr.Code
		title = fiberErr.Message
	}
	if responseErr := c.Status(code).JSON(fiber.Map{
		"type": "about:blank", "title": title,
		"status": code, "code": "request_failed", "detail": err.Error(), "requestId": c.GetRespHeader(fiber.HeaderXRequestID),
	}); responseErr != nil {
		return responseErr
	}
	c.Set(fiber.HeaderContentType, "application/problem+json; charset=utf-8")
	return nil
}
