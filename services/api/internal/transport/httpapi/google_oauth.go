package httpapi

import (
	"errors"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/gofiber/fiber/v3"
)

func googleOAuthCallback(service *googleoauth.Service, syncer SyncRequester) fiber.Handler {
	return func(c fiber.Ctx) error {
		if service == nil || !service.Configured() {
			return oauthProblem(googleoauth.ErrNotConfigured)
		}
		account, userID, err := service.CallbackWithOwner(c.Context(), c.Query("state"), c.Query("code"))
		if err != nil {
			return oauthProblem(err)
		}
		if syncer == nil {
			return c.Redirect().Status(fiber.StatusSeeOther).To("/settings/accounts?google=connected&sync=pending")
		}
		if _, err := syncer.StartInitial(c.Context(), userID, account.ID); err != nil && !errors.Is(err, mailflowsync.ErrRunExists) {
			return c.Redirect().Status(fiber.StatusSeeOther).To("/settings/accounts?google=connected&sync=pending")
		}
		return c.Redirect().Status(fiber.StatusSeeOther).To("/settings/accounts?google=connected")
	}
}

func googleOAuthStart(service *googleoauth.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if service == nil || !service.Configured() {
			return oauthProblem(googleoauth.ErrNotConfigured)
		}
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		var body struct {
			Reconsent bool `json:"reconsent"`
		}
		if len(c.Body()) > 0 && c.Bind().Body(&body) != nil {
			return newProblem(fiber.StatusBadRequest, "oauth_invalid_request", "Invalid OAuth request", "The OAuth request body is invalid.")
		}
		result, err := service.Start(c.Context(), user.ID, body.Reconsent)
		if err != nil {
			return oauthProblem(err)
		}
		return c.JSON(result)
	}
}

func googleOAuthRefresh(service *googleoauth.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if service == nil || !service.Configured() {
			return oauthProblem(googleoauth.ErrNotConfigured)
		}
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		account, err := service.Refresh(c.Context(), user.ID, c.Params("accountId"))
		if err != nil {
			return oauthProblem(err)
		}
		return c.JSON(account)
	}
}

func googleOAuthDisconnect(service *googleoauth.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if service == nil || !service.Configured() {
			return oauthProblem(googleoauth.ErrNotConfigured)
		}
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		result, err := service.Disconnect(c.Context(), user.ID, c.Params("accountId"))
		if err != nil {
			return oauthProblem(err)
		}
		return c.JSON(result)
	}
}

func oauthProblem(err error) error {
	switch {
	case errors.Is(err, googleoauth.ErrNotConfigured):
		return newProblem(fiber.StatusServiceUnavailable, "google_oauth_not_configured", "Google OAuth is not configured", "Add the Google OAuth client ID, client secret, and authorized redirect URI described in docs/providers/google.md.")
	case errors.Is(err, googleoauth.ErrInvalidState):
		return newProblem(fiber.StatusBadRequest, "oauth_state_invalid", "OAuth request expired", "Start the Google account connection again.")
	case errors.Is(err, googleoauth.ErrProvider), errors.Is(err, googleoauth.ErrInvalidToken):
		return newProblem(fiber.StatusBadGateway, "google_oauth_failed", "Google connection failed", "Google could not complete the account connection. Start the flow again or request consent again.")
	case errors.Is(err, accounts.ErrAccountNotFound):
		return newProblem(fiber.StatusNotFound, "account_not_found", "Account not found", "The requested account does not exist.")
	case errors.Is(err, googleoauth.ErrWrongProvider):
		return newProblem(fiber.StatusBadRequest, "account_provider_invalid", "Invalid account provider", "This operation is available only for Google accounts.")
	default:
		return newProblem(fiber.StatusInternalServerError, "google_oauth_failed", "Google connection failed", "The Google account operation could not be completed.")
	}
}
