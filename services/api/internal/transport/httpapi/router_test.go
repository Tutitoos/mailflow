package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	platformapp "github.com/Tutitoos/mailflow/services/api/internal/platform/app"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
)

func TestHealthAndEnglishFallback(t *testing.T) {
	app := platformapp.Build("test")

	response, err := app.Test(httptest.NewRequest("GET", "/health/live", nil))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("health check failed: status=%d err=%v", response.StatusCode, err)
	}

	response, err = app.Test(httptest.NewRequest("GET", "/api/v1/translations/fr", nil))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Messages map[string]string `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Messages["status.healthy"] != "All systems operational" {
		t.Fatalf("expected English fallback, got %q", payload.Messages["status.healthy"])
	}
}

func TestProblemDetailsAndSentryEnvelopeLimit(t *testing.T) {
	registry := metrics.NewRegistry()
	app := httpapi.New(httpapi.Dependencies{
		Admin:        admin.NewService("test", registry),
		Sentry:       mailflowsentry.NewService(4),
		Translations: translations.NewCatalog(),
	})

	response, err := app.Test(httptest.NewRequest("GET", "/missing", nil))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/problem+json") {
		t.Fatalf("expected Problem Details content type, got %q", contentType)
	}

	request := httptest.NewRequest("POST", "/sentry/api/1/envelope/", strings.NewReader("large"))
	response, err = app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 413 {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("expected 413, got %d: %s", response.StatusCode, body)
	}
}
