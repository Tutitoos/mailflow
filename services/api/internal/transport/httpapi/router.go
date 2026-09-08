package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/alerts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/backups"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	mailflowimap "github.com/Tutitoos/mailflow/services/api/internal/modules/imap"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/logs"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/microsoftoauth"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	jwtware "github.com/gofiber/contrib/v3/jwt"
	fibersentry "github.com/gofiber/contrib/v3/sentry"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/golang-jwt/jwt/v5"
)

type Dependencies struct {
	Accounts       AccountLister
	Actions        *mail.PendingActionService
	ActionState    mail.ActionStateStore
	Admin          *admin.Service
	Alerts         *alerts.Service
	Attachments    AttachmentService
	AuthAudience   string
	AuthIssuer     string
	AuthJWKSURL    string
	Backups        *backups.Repository
	CurrentUsers   authbridge.UserResolver
	Delivery       DraftService
	Events         EventStream
	GoogleOAuth    *googleoauth.Service
	IMAP           *mailflowimap.Service
	MicrosoftOAuth *microsoftoauth.Service
	Inbox          InboxReader
	Mailboxes      MailboxLabelReader
	Logs           *logs.Pipeline
	Metrics        *metrics.Registry
	Search         SearchReader
	Threads        ThreadReader
	Readiness      func(context.Context) error
	Sentry         *mailflowsentry.Service
	Translations   *translations.Catalog
	CaptureSentry  bool
	Shutdown       context.Context
	Sync           SyncRequester
}

type AccountLister interface {
	List(context.Context, string) ([]accounts.Account, error)
	Get(context.Context, string, string) (accounts.Account, error)
}

type SyncRequester interface {
	Request(context.Context, string, string) (mailflowsync.Run, error)
	StartInitial(context.Context, string, string) (mailflowsync.Run, error)
}

type InboxReader interface {
	ListInbox(context.Context, string, string, mail.Category, *mail.ThreadCursor, int) (mail.InboxPage, error)
}

type MailboxLabelReader interface {
	ListMailboxes(context.Context, string, string) ([]mail.Mailbox, error)
	ListLabels(context.Context, string, string) ([]mail.Label, error)
}

type ThreadReader interface {
	GetThread(context.Context, string, string, string) (mail.Thread, error)
	ListMessages(context.Context, string, string, string, *mail.MessageCursor, int) (mail.MessagePage, error)
}

type SearchReader interface {
	SearchMessages(context.Context, string, string, mail.SearchQuery, *mail.SearchCursor, int) (mail.SearchPage, error)
}

type DraftService interface {
	SaveDraft(context.Context, mail.CreateDraftInput) (mail.Draft, error)
	GetDraft(context.Context, string, string, string) (mail.Draft, error)
	UpdateDraft(context.Context, mail.UpdateDraftInput) (mail.Draft, error)
	CheckpointDraft(context.Context, string, string, string) (mail.Draft, error)
	DiscardDraft(context.Context, string, string, string) (mail.Draft, error)
	SendDraft(context.Context, string, string, string, int64, string) (mail.Delivery, error)
}

func New(deps Dependencies) *fiber.App {
	bodyLimit := fiber.DefaultBodyLimit
	if deps.Sentry != nil && bodyLimit < mailflowsentry.MaxArtifactBytes+(1<<20) {
		bodyLimit = mailflowsentry.MaxArtifactBytes + (1 << 20)
	}
	if deps.Attachments != nil {
		const multipartOverhead = 1 << 20
		maxInt := int64(^uint(0) >> 1)
		if maximum := deps.Attachments.MaxAttachmentBytes(); maximum > 0 && maximum <= maxInt-multipartOverhead {
			bodyLimit = int(maximum + multipartOverhead)
		}
	}
	app := fiber.New(fiber.Config{
		AppName:             "Mailflow API",
		BodyLimit:           bodyLimit,
		PassLocalsToContext: true,
		ReadTimeout:         15 * time.Second,
		WriteTimeout:        30 * time.Second,
		ErrorHandler:        problemHandler,
	})
	app.Use(recover.New(), requestid.New())
	if deps.Metrics != nil {
		app.Use(metricMiddleware(deps.Metrics))
	}

	app.Post("/sentry/api/1/envelope/", sentryIngest(deps.Sentry, deps.Metrics))
	app.Post("/sentry/api/1/store/", sentryIngest(deps.Sentry, deps.Metrics))
	app.Post("/api/0/organizations/mailflow/releases/", createSentryRelease(deps.Sentry))
	app.Post("/api/0/projects/mailflow/:component/releases/:version/files/", uploadSentryArtifact(deps.Sentry))
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
		catalog, err := deps.Translations.Get(c.Context(), c.Params("locale"))
		if err != nil {
			return newProblem(fiber.StatusServiceUnavailable, "translations_unavailable", "Translations unavailable", "The translation catalog is temporarily unavailable.")
		}
		return c.JSON(catalog)
	})
	v1.Get("/oauth/google/callback", googleOAuthCallback(deps.GoogleOAuth, deps.Sync))
	v1.Get("/oauth/microsoft/callback", microsoftOAuthCallback(deps.MicrosoftOAuth, deps.Sync))
	if deps.AuthJWKSURL != "" {
		if deps.AuthAudience == "" || deps.AuthIssuer == "" || deps.CurrentUsers == nil {
			panic("authenticated API requires audience, issuer, and current-user resolver")
		}
		v1.Use(websocketBearer, jwtware.New(jwtware.Config{
			Claims:     &jwt.RegisteredClaims{},
			JWKSetURLs: []string{deps.AuthJWKSURL},
			ParserOptions: []jwt.ParserOption{
				jwt.WithAudience(deps.AuthAudience),
				jwt.WithExpirationRequired(),
				jwt.WithIssuer(deps.AuthIssuer),
				jwt.WithValidMethods([]string{"EdDSA"}),
			},
			ErrorHandler: func(_ fiber.Ctx, _ error) error {
				return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
			},
			SuccessHandler: currentUserHandler(deps.CurrentUsers),
		}))
	}
	v1.Get("/me", func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		return c.JSON(user)
	})
	v1.Get("/events", eventEndpoint(deps.Events, deps.Shutdown, deps.AuthIssuer))
	v1.Get("/accounts", func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		if deps.Accounts == nil {
			return newProblem(fiber.StatusServiceUnavailable, "accounts_unavailable", "Accounts unavailable", "Account data is temporarily unavailable.")
		}
		items, err := deps.Accounts.List(c.Context(), user.ID)
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "accounts_failed", "Accounts unavailable", "Accounts could not be loaded.")
		}
		return c.JSON(fiber.Map{"items": items})
	})
	v1.Get("/mailboxes", listMailboxes(deps.Mailboxes))
	v1.Get("/labels", listLabels(deps.Mailboxes))
	v1.Get("/threads", listInbox(deps.Inbox))
	v1.Get("/threads/:threadId", getConversation(deps.Threads))
	v1.Get("/search", searchMail(deps.Search))
	v1.Post("/actions", createMailActions(deps.Actions, deps.ActionState, deps.Threads))
	v1.Post("/drafts", createDraft(deps.Delivery))
	v1.Get("/drafts/:draftId", getDraft(deps.Delivery))
	v1.Put("/drafts/:draftId", updateDraft(deps.Delivery))
	v1.Post("/drafts/:draftId/checkpoint", checkpointDraft(deps.Delivery))
	v1.Delete("/drafts/:draftId", discardDraft(deps.Delivery))
	v1.Post("/send", sendDraft(deps.Delivery))
	v1.Get("/oauth/google/status", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"configured": deps.GoogleOAuth != nil && deps.GoogleOAuth.Configured(), "setup": "docs/providers/google.md"})
	})
	v1.Post("/oauth/google/start", googleOAuthStart(deps.GoogleOAuth))
	v1.Get("/oauth/microsoft/status", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"configured": deps.MicrosoftOAuth != nil && deps.MicrosoftOAuth.Configured(), "setup": "docs/providers/microsoft.md"})
	})
	v1.Post("/oauth/microsoft/start", microsoftOAuthStart(deps.MicrosoftOAuth))
	v1.Post("/accounts/imap/probe", probeIMAPAccount(deps.IMAP))
	v1.Post("/accounts/imap", connectIMAPAccount(deps.IMAP))
	v1.Post("/accounts/icloud/probe", probeICloudAccount(deps.IMAP))
	v1.Post("/accounts/icloud", connectICloudAccount(deps.IMAP))
	v1.Post("/accounts/:accountId/imap/folders/discover", discoverIMAPFolders(deps.IMAP, deps.Sync))
	v1.Post("/accounts/:accountId/refresh", refreshAccount(deps.Accounts, deps.GoogleOAuth, deps.MicrosoftOAuth))
	v1.Post("/accounts/:accountId/sync", synchronizeAccount(deps.Sync))
	v1.Delete("/accounts/:accountId", disconnectOAuthAccount(deps.Accounts, deps.GoogleOAuth, deps.MicrosoftOAuth, deps.IMAP))
	v1.Post("/attachments", attachmentUpload(deps.Attachments))
	v1.Get("/attachments/:attachmentId", attachmentDownload(deps.Attachments))
	adminRoutes := v1.Group("/admin")
	adminRoutes.Use(requireAdminOwner)
	adminRoutes.Get("/status", adminStatus(deps.Admin))
	adminRoutes.Get("/queue", adminQueue(deps.Admin))
	adminRoutes.Post("/queue/retry", retryAdminQueue(deps.Admin, deps.Sync))
	adminRoutes.Get("/cdn", adminCDNStatus(deps.Admin))
	adminRoutes.Get("/backups", adminBackups(deps.Backups))
	adminRoutes.Get("/alerts", adminAlerts(deps.Alerts))
	adminRoutes.Post("/alerts/test", testAdminAlert(deps.Alerts))
	adminRoutes.Get("/metrics", adminMetrics(deps.Admin))
	adminRoutes.Get("/logs", adminLogs(deps.Logs))
	adminRoutes.Get("/logs/debug", logDebugStatus(deps.Logs))
	adminRoutes.Put("/logs/debug", setLogDebug(deps.Logs))
	adminRoutes.Get("/sentry", adminSentryIssues(deps.Sentry))
	adminRoutes.Get("/sentry/telemetry", adminSentryTelemetry(deps.Sentry))
	adminRoutes.Put("/sentry/:issueId", setAdminSentryIssueStatus(deps.Sentry))
	adminRoutes.Get("/translations", exportTranslations(deps.Translations))
	adminRoutes.Post("/translations/validate", validateTranslations(deps.Translations))
	adminRoutes.Put("/translations", updateTranslations(deps.Translations))

	return app
}

func metricMiddleware(registry *metrics.Registry) fiber.Handler {
	return func(c fiber.Ctx) error {
		started := time.Now()
		err := c.Next()
		status := c.Response().StatusCode()
		if err != nil {
			status = metricErrorStatus(err)
		}
		result := "success"
		if status >= 500 {
			result = "server_error"
		} else if status >= 400 {
			result = "client_error"
		}
		labels := map[string]string{"service": "api", "module": "http", "operation": strings.ToLower(c.Method()), "result": result}
		_ = registry.Add("mailflow_http_requests_total", 1, labels)
		_ = registry.Observe("mailflow_http_request_duration_seconds", time.Since(started).Seconds(), labels)
		return err
	}
}

func metricErrorStatus(err error) int {
	var problem *problemError
	if errors.As(err, &problem) {
		return problem.status
	}
	var fiberError *fiber.Error
	if errors.As(err, &fiberError) {
		return fiberError.Code
	}
	return fiber.StatusInternalServerError
}

func adminMetrics(service *admin.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		now := time.Now().UTC()
		resolution := metrics.Resolution(c.Query("resolution", string(metrics.Minute)))
		from, err := metricTime(c.Query("from"), now.Add(-time.Hour))
		if err != nil {
			return invalidMetricsQuery()
		}
		until, err := metricTime(c.Query("until"), now.Add(time.Minute))
		if err != nil {
			return invalidMetricsQuery()
		}
		limit := metrics.DefaultQueryLimit
		if raw := c.Query("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil {
				return invalidMetricsQuery()
			}
		}
		items, err := service.Metrics(c.Context(), metrics.Query{Resolution: resolution, Name: c.Query("name"), From: from, Until: until, Limit: limit})
		if errors.Is(err, metrics.ErrInvalidMetric) {
			return invalidMetricsQuery()
		}
		if err != nil {
			return newProblem(fiber.StatusServiceUnavailable, "metrics_unavailable", "Metrics unavailable", "Metric series could not be loaded.")
		}
		return c.JSON(fiber.Map{"items": items})
	}
}

func metricTime(raw string, fallback time.Time) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	return time.Parse(time.RFC3339, raw)
}

func invalidMetricsQuery() error {
	return newProblem(fiber.StatusBadRequest, "invalid_metrics_query", "Invalid metrics query", "The requested resolution, range, name, or limit is invalid.")
}

func currentUserHandler(users authbridge.UserResolver) fiber.Handler {
	return func(c fiber.Ctx) error {
		token := jwtware.FromContext(c)
		claims, ok := token.Claims.(*jwt.RegisteredClaims)
		if !ok || claims.Subject == "" {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		user, err := users.FindBySubject(c.Context(), claims.Subject)
		if errors.Is(err, authbridge.ErrInvalidSubject) || errors.Is(err, authbridge.ErrUserNotFound) {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		if err != nil {
			return newProblem(fiber.StatusServiceUnavailable, "authentication_unavailable", "Authentication unavailable", "The user profile could not be loaded.")
		}
		c.SetContext(authbridge.WithUser(c.Context(), user))
		return c.Next()
	}
}

type problemError struct {
	status int
	code   string
	title  string
	detail string
}

func (err *problemError) Error() string { return fmt.Sprintf("%s: %s", err.code, err.detail) }

func newProblem(status int, code, title, detail string) error {
	return &problemError{status: status, code: code, title: title, detail: detail}
}

func sentryIngest(service *mailflowsentry.Service, registry *metrics.Registry) fiber.Handler {
	return func(c fiber.Ctx) error {
		if service == nil {
			return fiber.NewError(fiber.StatusServiceUnavailable, "Sentry ingestion unavailable")
		}
		authorization := c.Get("X-Sentry-Auth")
		if authorization == "" {
			authorization = c.Get("Authorization")
		}
		receipt, err := service.Ingest(c.Context(), mailflowsentry.Request{
			Authorization: authorization, QueryKey: c.Query("sentry_key"),
			LegacyEventID: c.Get("X-Sentry-Event-ID"), Body: c.Body(),
			Legacy: strings.HasSuffix(c.Path(), "/store/"),
		})
		result := "success"
		switch {
		case errors.Is(err, mailflowsentry.ErrEnvelopeTooLarge):
			result = "rejected"
			observeSentryIngest(registry, result, len(c.Body()))
			return fiber.NewError(fiber.StatusRequestEntityTooLarge, err.Error())
		case errors.Is(err, mailflowsentry.ErrInvalidEnvelope):
			result = "rejected"
			observeSentryIngest(registry, result, len(c.Body()))
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		case errors.Is(err, mailflowsentry.ErrUnauthenticated):
			result = "rejected"
			observeSentryIngest(registry, result, len(c.Body()))
			return fiber.NewError(fiber.StatusUnauthorized, err.Error())
		case errors.Is(err, mailflowsentry.ErrRateLimited):
			result = "rejected"
			observeSentryIngest(registry, result, len(c.Body()))
			c.Set("Retry-After", "60")
			return fiber.NewError(fiber.StatusTooManyRequests, err.Error())
		case errors.Is(err, mailflowsentry.ErrStorageQuota):
			result = "rejected"
			observeSentryIngest(registry, result, len(c.Body()))
			return fiber.NewError(fiber.StatusInsufficientStorage, err.Error())
		case errors.Is(err, mailflowsentry.ErrUnavailable):
			result = "failure"
			observeSentryIngest(registry, result, len(c.Body()))
			return fiber.NewError(fiber.StatusServiceUnavailable, err.Error())
		case err != nil:
			result = "failure"
			observeSentryIngest(registry, result, len(c.Body()))
			return fiber.NewError(fiber.StatusInternalServerError, "Sentry ingestion failed")
		}
		observeSentryIngest(registry, result, len(c.Body()))
		return c.JSON(receipt)
	}
}

func observeSentryIngest(registry *metrics.Registry, result string, size int) {
	if registry == nil {
		return
	}
	labels := map[string]string{"service": "api", "module": "sentry", "operation": "ingest", "result": result}
	_ = registry.Add("mailflow_sentry_envelopes_total", 1, labels)
	_ = registry.Observe("mailflow_sentry_envelope_bytes", float64(size), labels)
}

func problemHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	title := fiber.ErrInternalServerError.Message
	problemCode := "request_failed"
	detail := err.Error()
	var problem *problemError
	if errors.As(err, &problem) {
		code = problem.status
		title = problem.title
		problemCode = problem.code
		detail = problem.detail
	}
	if fiberErr, ok := err.(*fiber.Error); ok {
		code = fiberErr.Code
		title = fiberErr.Message
	}
	if responseErr := c.Status(code).JSON(fiber.Map{
		"type": "about:blank", "title": title,
		"status": code, "code": problemCode, "detail": detail, "requestId": requestid.FromContext(c),
	}); responseErr != nil {
		return responseErr
	}
	c.Set(fiber.HeaderContentType, "application/problem+json; charset=utf-8")
	return nil
}
