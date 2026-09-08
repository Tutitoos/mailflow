package httpapi

import (
	"errors"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/gofiber/fiber/v3"
)

func exportTranslations(catalog *translations.Catalog) fiber.Handler {
	return func(c fiber.Ctx) error {
		exported, err := catalog.Export(c.Context())
		if err != nil {
			return translationsUnavailable()
		}
		return c.JSON(exported)
	}
}

func validateTranslations(catalog *translations.Catalog) fiber.Handler {
	return func(c fiber.Ctx) error {
		var request translations.UpdateRequest
		if c.Bind().Body(&request) != nil {
			return invalidTranslations("The translation update could not be decoded.")
		}
		exported, err := catalog.Validate(c.Context(), request)
		if errors.Is(err, translations.ErrConflict) {
			return translationConflict()
		}
		if err != nil {
			return translationsUnavailable()
		}
		return c.JSON(exported)
	}
}

func updateTranslations(catalog *translations.Catalog) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		var request translations.UpdateRequest
		if c.Bind().Body(&request) != nil {
			return invalidTranslations("The translation update could not be decoded.")
		}
		result, err := catalog.Update(c.Context(), user.ID, request)
		if errors.Is(err, translations.ErrConflict) {
			return translationConflict()
		}
		if errors.Is(err, translations.ErrInvalidCatalog) {
			return invalidTranslations("The catalog contains missing, stale, private, unknown, or invalid ICU entries.")
		}
		if err != nil {
			return translationsUnavailable()
		}
		return c.JSON(result)
	}
}

func translationConflict() error {
	return newProblem(fiber.StatusConflict, "translation_revision_conflict", "Translation revision conflict", "Reload the current catalog before applying this update.")
}

func invalidTranslations(detail string) error {
	return newProblem(fiber.StatusUnprocessableEntity, "translation_catalog_invalid", "Invalid translation catalog", detail)
}

func translationsUnavailable() error {
	return newProblem(fiber.StatusServiceUnavailable, "translations_unavailable", "Translations unavailable", "The translation catalog is temporarily unavailable.")
}
