package httpapi

import (
	"errors"
	"io"
	"strings"
	"time"

	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/gofiber/fiber/v3"
)

func createSentryRelease(service *mailflowsentry.Service) fiber.Handler {
	type requestBody struct {
		Version  string   `json:"version"`
		Projects []string `json:"projects"`
	}
	return func(c fiber.Ctx) error {
		if service == nil {
			return sentryReleaseUnavailable()
		}
		var request requestBody
		if c.Bind().Body(&request) != nil || len(request.Projects) > 1 {
			return invalidSentryRelease()
		}
		component := ""
		if len(request.Projects) == 1 {
			component = request.Projects[0]
		}
		release, err := service.CreateRelease(c.Context(), c.Get("Authorization"), component, request.Version, time.Now().UTC())
		if problem := sentryReleaseError(err); problem != nil {
			return problem
		}
		return c.Status(fiber.StatusCreated).JSON(release)
	}
}

func uploadSentryArtifact(service *mailflowsentry.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if service == nil {
			return sentryReleaseUnavailable()
		}
		header, err := c.FormFile("file")
		if err != nil || header.Size <= 0 {
			return invalidSentryArtifact()
		}
		if header.Size > mailflowsentry.MaxArtifactBytes {
			return sentryArtifactTooLarge()
		}
		file, err := header.Open()
		if err != nil {
			return sentryReleaseUnavailable()
		}
		defer file.Close()
		payload, err := io.ReadAll(io.LimitReader(file, mailflowsentry.MaxArtifactBytes+1))
		if err != nil {
			return sentryReleaseUnavailable()
		}
		if len(payload) > mailflowsentry.MaxArtifactBytes {
			return sentryArtifactTooLarge()
		}
		name := strings.TrimSpace(c.FormValue("name"))
		if name == "" {
			name = header.Filename
		}
		artifact, err := service.UploadArtifact(c.Context(), c.Get("Authorization"), c.Params("component"), c.Params("version"), name, payload, time.Now().UTC())
		if problem := sentryReleaseError(err); problem != nil {
			return problem
		}
		return c.Status(fiber.StatusCreated).JSON(artifact)
	}
}

func sentryReleaseError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mailflowsentry.ErrUnauthenticated):
		return newProblem(fiber.StatusUnauthorized, "sentry_artifact_unauthorized", "Sentry artifact unauthorized", "A valid component upload token is required.")
	case errors.Is(err, mailflowsentry.ErrInvalidRelease):
		return invalidSentryRelease()
	case errors.Is(err, mailflowsentry.ErrInvalidArtifact):
		return invalidSentryArtifact()
	case errors.Is(err, mailflowsentry.ErrStorageQuota):
		return newProblem(fiber.StatusInsufficientStorage, "sentry_artifact_quota", "Sentry artifact quota exceeded", "The configured artifact storage quota has been reached.")
	case errors.Is(err, mailflowsentry.ErrUnavailable):
		return sentryReleaseUnavailable()
	default:
		return sentryReleaseUnavailable()
	}
}

func invalidSentryRelease() error {
	return newProblem(fiber.StatusBadRequest, "invalid_sentry_release", "Invalid Sentry release", "The release version or project is invalid.")
}

func invalidSentryArtifact() error {
	return newProblem(fiber.StatusBadRequest, "invalid_sentry_artifact", "Invalid Sentry artifact", "The uploaded release artifact is invalid or unsupported.")
}

func sentryArtifactTooLarge() error {
	return newProblem(fiber.StatusRequestEntityTooLarge, "sentry_artifact_too_large", "Sentry artifact too large", "The artifact exceeds the configured 20 MiB limit.")
}

func sentryReleaseUnavailable() error {
	return newProblem(fiber.StatusServiceUnavailable, "sentry_releases_unavailable", "Sentry releases unavailable", "Release artifact processing is temporarily unavailable.")
}
