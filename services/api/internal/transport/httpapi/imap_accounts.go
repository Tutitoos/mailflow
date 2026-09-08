package httpapi

import (
	"errors"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	mailflowimap "github.com/Tutitoos/mailflow/services/api/internal/modules/imap"
	"github.com/gofiber/fiber/v3"
)

func probeIMAPAccount(service *mailflowimap.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		input, err := bindIMAPInput(c, service)
		if err != nil {
			return err
		}
		result, err := service.Probe(c.Context(), input)
		if err != nil {
			return imapProblem(err)
		}
		return c.JSON(result)
	}
}

func connectIMAPAccount(service *mailflowimap.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		input, err := bindIMAPInput(c, service)
		if err != nil {
			return err
		}
		account, err := service.Connect(c.Context(), input)
		if err != nil {
			return imapProblem(err)
		}
		return c.Status(fiber.StatusCreated).JSON(account)
	}
}

func bindIMAPInput(c fiber.Ctx, service *mailflowimap.Service) (mailflowimap.ConnectInput, error) {
	if service == nil {
		return mailflowimap.ConnectInput{}, newProblem(fiber.StatusServiceUnavailable, "imap_unavailable", "IMAP is unavailable", "IMAP account management is temporarily unavailable.")
	}
	user, ok := authbridge.UserFromContext(c.Context())
	if !ok {
		return mailflowimap.ConnectInput{}, newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
	}
	var input mailflowimap.ConnectInput
	if err := c.Bind().Body(&input); err != nil {
		return mailflowimap.ConnectInput{}, newProblem(fiber.StatusBadRequest, "imap_configuration_invalid", "Invalid mail server configuration", "Provide valid encrypted IMAP and SMTP server settings.")
	}
	input.UserID = user.ID
	return input, nil
}

func imapProblem(err error) error {
	switch {
	case errors.Is(err, mailflowimap.ErrInvalidConfiguration):
		return newProblem(fiber.StatusBadRequest, "imap_configuration_invalid", "Invalid mail server configuration", "Provide valid encrypted IMAP and SMTP server settings.")
	case errors.Is(err, mailflowimap.ErrTLSIdentity):
		return newProblem(fiber.StatusUnprocessableEntity, "mail_tls_identity_failed", "Mail server identity could not be verified", "Check the server hostname and certificate chain. Certificate verification cannot be bypassed.")
	case errors.Is(err, mailflowimap.ErrTimeout):
		return newProblem(fiber.StatusGatewayTimeout, "mail_server_timeout", "Mail server timed out", "Check the server address, ports, and network reachability.")
	case errors.Is(err, mailflowimap.ErrAuthentication):
		return newProblem(fiber.StatusUnauthorized, "mail_authentication_failed", "Mail server authentication failed", "Check the username and app-specific password.")
	case errors.Is(err, mailflowimap.ErrCapability):
		return newProblem(fiber.StatusUnprocessableEntity, "mail_capability_failed", "Mail server capabilities are unsupported", "The server did not expose the secure protocol capabilities Mailflow requires.")
	case errors.Is(err, mailflowimap.ErrProtocol):
		return newProblem(fiber.StatusBadGateway, "mail_protocol_failed", "Mail server protocol failed", "The server returned an invalid IMAP or SMTP response.")
	case errors.Is(err, mailflowimap.ErrWrongProvider):
		return newProblem(fiber.StatusBadRequest, "account_provider_invalid", "Invalid account provider", "This operation is available only for IMAP accounts.")
	case errors.Is(err, accounts.ErrAccountNotFound):
		return accountOAuthProblem(err)
	default:
		return newProblem(fiber.StatusInternalServerError, "imap_operation_failed", "IMAP operation failed", "The IMAP account operation could not be completed.")
	}
}
