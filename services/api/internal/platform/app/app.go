package app

import (
	"context"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/alerts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/backups"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/logs"
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
	Admin         *admin.Service
	Alerts        *alerts.Service
	ActionState   mail.ActionStateStore
	Attachments   *cdn.Service
	AuthAudience  string
	AuthIssuer    string
	AuthJWKSURL   string
	Backups       *backups.Repository
	CurrentUsers  authbridge.UserResolver
	Delivery      *mail.DeliveryService
	Events        *events.Store
	GoogleOAuth   *googleoauth.Service
	Inbox         *mail.ThreadRepositoryStore
	Mailboxes     *mail.MailboxLabelRepositoryStore
	Logs          *logs.Pipeline
	Metrics       *metrics.Service
	Search        *mail.ThreadRepositoryStore
	Sentry        *sentry.Service
	Threads       *mail.ThreadRepositoryStore
	Translations  *translations.Catalog
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
	if runtimeOptions.Sentry == nil {
		runtimeOptions.Sentry = sentry.NewService(sentry.DefaultMaxEnvelopeBytes)
	}
	if runtimeOptions.Translations == nil {
		runtimeOptions.Translations = translations.NewCatalog()
	}
	if runtimeOptions.Admin == nil {
		runtimeOptions.Admin = admin.NewServiceWithMetrics(version, metricService)
	}
	_ = metricService.Registry().Set("mailflow_build_info", "gauge", 1, map[string]string{"service": "api", "result": "ready"})
	return httpapi.New(httpapi.Dependencies{
		Accounts:      runtimeOptions.Accounts,
		Actions:       runtimeOptions.Actions,
		ActionState:   runtimeOptions.ActionState,
		Attachments:   attachmentService,
		Admin:         runtimeOptions.Admin,
		Alerts:        runtimeOptions.Alerts,
		AuthAudience:  runtimeOptions.AuthAudience,
		AuthIssuer:    runtimeOptions.AuthIssuer,
		AuthJWKSURL:   runtimeOptions.AuthJWKSURL,
		Backups:       runtimeOptions.Backups,
		CurrentUsers:  runtimeOptions.CurrentUsers,
		Delivery:      runtimeOptions.Delivery,
		Events:        runtimeOptions.Events,
		GoogleOAuth:   runtimeOptions.GoogleOAuth,
		Inbox:         runtimeOptions.Inbox,
		Mailboxes:     runtimeOptions.Mailboxes,
		Logs:          runtimeOptions.Logs,
		Metrics:       metricService.Registry(),
		Search:        runtimeOptions.Search,
		Threads:       runtimeOptions.Threads,
		Readiness:     runtimeOptions.Readiness,
		Sentry:        runtimeOptions.Sentry,
		Translations:  runtimeOptions.Translations,
		CaptureSentry: runtimeOptions.SentryEnabled,
		Shutdown:      runtimeOptions.Shutdown,
		Sync:          runtimeOptions.Sync,
	})
}
