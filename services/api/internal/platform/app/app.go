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
	Actions       *mail.PendingActionService
	ActionState   mail.ActionStateStore
	Attachments   *cdn.Service
	AuthAudience  string
	AuthIssuer    string
	AuthJWKSURL   string
	CurrentUsers  authbridge.UserResolver
	Delivery      *mail.DeliveryService
	Events        *events.Store
	GoogleOAuth   *googleoauth.Service
	Inbox         *mail.ThreadRepositoryStore
	Mailboxes     *mail.MailboxLabelRepositoryStore
	Metrics       *metrics.Service
	Search        *mail.ThreadRepositoryStore
	Threads       *mail.ThreadRepositoryStore
	Readiness     func(context.Context) error
	SentryEnabled bool
	Shutdown      context.Context
	Sync          *mailflowsync.Scheduler
}

func Build(version string, options ...Options) *fiber.App {
	var runtimeOptions Options
	if len(options) > 0 {
		runtimeOptions = options[0]
	}
	var attachmentService httpapi.AttachmentService
	if runtimeOptions.Attachments != nil {
		attachmentService = runtimeOptions.Attachments
	}
	metricService := runtimeOptions.Metrics
	if metricService == nil {
		metricService = metrics.NewService(metrics.NewRegistry(), nil)
	}
	_ = metricService.Registry().Set("mailflow_build_info", "gauge", 1, map[string]string{"service": "api", "result": "ready"})
	return httpapi.New(httpapi.Dependencies{
		Accounts:      runtimeOptions.Accounts,
		Actions:       runtimeOptions.Actions,
		ActionState:   runtimeOptions.ActionState,
		Attachments:   attachmentService,
		Admin:         admin.NewServiceWithMetrics(version, metricService),
		AuthAudience:  runtimeOptions.AuthAudience,
		AuthIssuer:    runtimeOptions.AuthIssuer,
		AuthJWKSURL:   runtimeOptions.AuthJWKSURL,
		CurrentUsers:  runtimeOptions.CurrentUsers,
		Delivery:      runtimeOptions.Delivery,
		Events:        runtimeOptions.Events,
		GoogleOAuth:   runtimeOptions.GoogleOAuth,
		Inbox:         runtimeOptions.Inbox,
		Mailboxes:     runtimeOptions.Mailboxes,
		Metrics:       metricService.Registry(),
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
