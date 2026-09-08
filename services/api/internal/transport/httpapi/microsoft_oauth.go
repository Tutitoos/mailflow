package httpapi

import (
	"errors"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/googleoauth"
	mailflowimap "github.com/Tutitoos/mailflow/services/api/internal/modules/imap"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/microsoftoauth"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/gofiber/fiber/v3"
)

func microsoftOAuthCallback(service *microsoftoauth.Service, syncer SyncRequester) fiber.Handler {
	return func(c fiber.Ctx) error {
		if service == nil || !service.Configured() {
			return microsoftOAuthProblem(microsoftoauth.ErrNotConfigured)
		}
		account, userID, desktop, err := service.CallbackWithClient(c.Context(), c.Query("state"), c.Query("code"), c.Query("error"))
		if err != nil {
			if desktop {
				return c.Redirect().Status(fiber.StatusSeeOther).To("mailflow://open/settings/accounts?microsoft=failed")
			}
			return microsoftOAuthProblem(err)
		}
		if syncer == nil {
			return microsoftOAuthRedirect(c, desktop, true)
		}
		if _, err := syncer.StartInitial(c.Context(), userID, account.ID); err != nil && !errors.Is(err, mailflowsync.ErrRunExists) {
			return microsoftOAuthRedirect(c, desktop, true)
		}
		return microsoftOAuthRedirect(c, desktop, false)
	}
}

func microsoftOAuthStart(service *microsoftoauth.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if service == nil || !service.Configured() {
			return microsoftOAuthProblem(microsoftoauth.ErrNotConfigured)
		}
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		var body struct {
			Reconsent bool `json:"reconsent"`
			Desktop   bool `json:"desktop"`
		}
		if len(c.Body()) > 0 && c.Bind().Body(&body) != nil {
			return newProblem(fiber.StatusBadRequest, "oauth_invalid_request", "Invalid OAuth request", "The OAuth request body is invalid.")
		}
		result, err := service.StartForClient(c.Context(), user.ID, body.Reconsent, body.Desktop)
		if err != nil {
			return microsoftOAuthProblem(err)
		}
		return c.JSON(result)
	}
}

func microsoftOAuthRedirect(c fiber.Ctx, desktop, syncPending bool) error {
	location := "/settings/accounts?microsoft=connected"
	if desktop {
		location = "mailflow://open/settings/accounts?microsoft=connected"
	}
	if syncPending {
		location += "&sync=pending"
	}
	return c.Redirect().Status(fiber.StatusSeeOther).To(location)
}

func refreshAccount(accountStore AccountLister, google *googleoauth.Service, microsoft *microsoftoauth.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		if accountStore == nil {
			return newProblem(fiber.StatusServiceUnavailable, "accounts_unavailable", "Accounts unavailable", "Account data is temporarily unavailable.")
		}
		account, err := accountStore.Get(c.Context(), user.ID, c.Params("accountId"))
		if err != nil {
			return accountOAuthProblem(err)
		}
		if account.SyncState == accounts.SyncDisabled {
			return newProblem(fiber.StatusConflict, "account_disabled", "Account is disconnected", "Reconnect the account before refreshing provider access.")
		}
		switch account.Provider {
		case accounts.ProviderGoogle:
			account, err = google.Refresh(c.Context(), user.ID, account.ID)
			if err != nil {
				return googleOAuthProblem(err)
			}
		case accounts.ProviderMicrosoft:
			account, err = microsoft.Refresh(c.Context(), user.ID, account.ID)
			if err != nil {
				return microsoftOAuthProblem(err)
			}
		default:
			return newProblem(fiber.StatusBadRequest, "account_provider_invalid", "Invalid account provider", "This account does not support OAuth refresh.")
		}
		return c.JSON(account)
	}
}

func disconnectOAuthAccount(accountStore AccountLister, google *googleoauth.Service, microsoft *microsoftoauth.Service, imapService *mailflowimap.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		if accountStore == nil {
			return newProblem(fiber.StatusServiceUnavailable, "accounts_unavailable", "Accounts unavailable", "Account data is temporarily unavailable.")
		}
		account, err := accountStore.Get(c.Context(), user.ID, c.Params("accountId"))
		if err != nil {
			return accountOAuthProblem(err)
		}
		switch account.Provider {
		case accounts.ProviderGoogle:
			result, err := google.Disconnect(c.Context(), user.ID, account.ID)
			if err != nil {
				return googleOAuthProblem(err)
			}
			return c.JSON(result)
		case accounts.ProviderMicrosoft:
			result, err := microsoft.Disconnect(c.Context(), user.ID, account.ID)
			if err != nil {
				return microsoftOAuthProblem(err)
			}
			return c.JSON(result)
		case accounts.ProviderIMAP:
			if imapService == nil {
				return newProblem(fiber.StatusServiceUnavailable, "imap_unavailable", "IMAP is unavailable", "IMAP account management is temporarily unavailable.")
			}
			result, err := imapService.Disconnect(c.Context(), user.ID, account.ID)
			if err != nil {
				return imapProblem(err)
			}
			return c.JSON(result)
		default:
			return newProblem(fiber.StatusBadRequest, "account_provider_invalid", "Invalid account provider", "This account does not support OAuth disconnect.")
		}
	}
}

func accountOAuthProblem(err error) error {
	if errors.Is(err, accounts.ErrAccountNotFound) {
		return newProblem(fiber.StatusNotFound, "account_not_found", "Account not found", "The requested account does not exist.")
	}
	return newProblem(fiber.StatusInternalServerError, "account_operation_failed", "Account operation failed", "The account operation could not be completed.")
}

func microsoftOAuthProblem(err error) error {
	switch {
	case errors.Is(err, microsoftoauth.ErrNotConfigured):
		return newProblem(fiber.StatusServiceUnavailable, "microsoft_oauth_not_configured", "Microsoft OAuth is not configured", "Add the Microsoft application ID, client secret, authority, and redirect URI described in docs/providers/microsoft.md.")
	case errors.Is(err, microsoftoauth.ErrInvalidState):
		return newProblem(fiber.StatusBadRequest, "oauth_state_invalid", "OAuth request expired", "Start the Microsoft account connection again.")
	case errors.Is(err, microsoftoauth.ErrConsentRevoked), errors.Is(err, microsoftoauth.ErrReconsentNeeded):
		return newProblem(fiber.StatusUnauthorized, "microsoft_reconsent_required", "Microsoft consent is required", "Reconnect the account and grant the requested Mailflow permissions again.")
	case errors.Is(err, microsoftoauth.ErrTenantPolicy):
		return newProblem(fiber.StatusForbidden, "microsoft_tenant_policy", "Microsoft tenant policy blocked access", "Ask the Microsoft 365 administrator to allow this application, or connect an account permitted by the installation authority.")
	case errors.Is(err, microsoftoauth.ErrAccessDenied):
		return newProblem(fiber.StatusForbidden, "microsoft_access_denied", "Microsoft access was denied", "Start the connection again when you are ready to grant Mailflow access.")
	case errors.Is(err, microsoftoauth.ErrProvider), errors.Is(err, microsoftoauth.ErrInvalidToken):
		return newProblem(fiber.StatusBadGateway, "microsoft_oauth_failed", "Microsoft connection failed", "Microsoft could not complete the account operation. Start the flow again or request consent again.")
	case errors.Is(err, accounts.ErrAccountNotFound):
		return accountOAuthProblem(err)
	case errors.Is(err, microsoftoauth.ErrWrongProvider):
		return newProblem(fiber.StatusBadRequest, "account_provider_invalid", "Invalid account provider", "This operation is available only for Microsoft accounts.")
	default:
		return newProblem(fiber.StatusInternalServerError, "microsoft_oauth_failed", "Microsoft connection failed", "The Microsoft account operation could not be completed.")
	}
}
