package httpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
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
	Accounts      AccountLister
	Admin         *admin.Service
	Attachments   AttachmentReader
	AuthAudience  string
	AuthIssuer    string
	AuthJWKSURL   string
	CurrentUsers  authbridge.UserResolver
	Events        EventStream
	GoogleOAuth   *googleoauth.Service
	Inbox         InboxReader
	Mailboxes     MailboxLabelReader
	Search        SearchReader
	Threads       ThreadReader
	Readiness     func(context.Context) error
	Sentry        *mailflowsentry.Service
	Translations  *translations.Catalog
	CaptureSentry bool
	Shutdown      context.Context
	Sync          SyncRequester
}

type AccountLister interface {
	List(context.Context, string) ([]accounts.Account, error)
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
	v1.Get("/oauth/google/callback", googleOAuthCallback(deps.GoogleOAuth, deps.Sync))
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
	v1.Get("/oauth/google/status", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"configured": deps.GoogleOAuth != nil && deps.GoogleOAuth.Configured(), "setup": "docs/providers/google.md"})
	})
	v1.Post("/oauth/google/start", googleOAuthStart(deps.GoogleOAuth))
	v1.Post("/accounts/:accountId/refresh", googleOAuthRefresh(deps.GoogleOAuth))
	v1.Post("/accounts/:accountId/sync", synchronizeAccount(deps.Sync))
	v1.Delete("/accounts/:accountId", googleOAuthDisconnect(deps.GoogleOAuth))
	v1.Get("/attachments/:attachmentId", attachmentDownload(deps.Attachments))
	adminRoutes := v1.Group("/admin")
	adminRoutes.Get("/status", func(c fiber.Ctx) error { return c.JSON(deps.Admin.Status()) })
	adminRoutes.Get("/metrics", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"items": deps.Admin.Metrics()}) })

	return app
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
