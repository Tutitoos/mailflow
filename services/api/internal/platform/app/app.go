package app

import (
	"context"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
	"github.com/gofiber/fiber/v3"
)

type Options struct {
	Accounts      *accounts.Service
	Attachments   *cdn.Service
	AuthAudience  string
	AuthIssuer    string
	AuthJWKSURL   string
	CurrentUsers  authbridge.UserResolver
	Events        *events.Store
	GoogleOAuth   *googleoauth.Service
	Inbox         *mail.ThreadRepositoryStore
	Mailboxes     *mail.MailboxLabelRepositoryStore
	Search        *mail.ThreadRepositoryStore
	Threads       *mail.ThreadRepositoryStore
	Readiness     func(context.Context) error
	SentryEnabled bool
	Shutdown      context.Context
	Sync          *mailflowsync.Scheduler
}

func Build(version string, options ...Options) *fiber.App {
	registry := metrics.NewRegistry()
	_ = registry.Set("mailflow_build_info", "gauge", 1, map[string]string{"service": "api", "result": "ready"})
	var runtimeOptions Options
	if len(options) > 0 {
		runtimeOptions = options[0]
	}
	return httpapi.New(httpapi.Dependencies{
		Accounts:      runtimeOptions.Accounts,
		Attachments:   runtimeOptions.Attachments,
		Admin:         admin.NewService(version, registry),
		AuthAudience:  runtimeOptions.AuthAudience,
		AuthIssuer:    runtimeOptions.AuthIssuer,
		AuthJWKSURL:   runtimeOptions.AuthJWKSURL,
		CurrentUsers:  runtimeOptions.CurrentUsers,
		Events:        runtimeOptions.Events,
		GoogleOAuth:   runtimeOptions.GoogleOAuth,
		Inbox:         runtimeOptions.Inbox,
		Mailboxes:     runtimeOptions.Mailboxes,
		Search:        runtimeOptions.Search,
		Threads:       runtimeOptions.Threads,
		Readiness:     runtimeOptions.Readiness,
		Sentry:        sentry.NewService(5 << 20),
		Translations:  translations.NewCatalog(),
		CaptureSentry: runtimeOptions.SentryEnabled,
		Shutdown:      runtimeOptions.Shutdown,
		Sync:          runtimeOptions.Sync,
	})
}
