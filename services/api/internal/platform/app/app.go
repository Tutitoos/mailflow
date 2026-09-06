package app

import (
	"context"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
	"github.com/gofiber/fiber/v3"
)

type Options struct {
	AuthJWKSURL   string
	Readiness     func(context.Context) error
	SentryEnabled bool
}

func Build(version string, options ...Options) *fiber.App {
	registry := metrics.NewRegistry()
	_ = registry.Set("mailflow_build_info", "gauge", 1, map[string]string{"service": "api", "result": "ready"})
	var runtimeOptions Options
	if len(options) > 0 {
		runtimeOptions = options[0]
	}
	return httpapi.New(httpapi.Dependencies{
		Admin:         admin.NewService(version, registry),
		AuthJWKSURL:   runtimeOptions.AuthJWKSURL,
		Readiness:     runtimeOptions.Readiness,
		Sentry:        sentry.NewService(5 << 20),
		Translations:  translations.NewCatalog(),
		CaptureSentry: runtimeOptions.SentryEnabled,
	})
}
