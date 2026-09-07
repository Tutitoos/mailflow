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

func TestAdminMetricsValidatesAndReturnsBoundedSeries(t *testing.T) {
	registry := metrics.NewRegistry()
	if err := registry.Observe("mailflow_http_request_duration_seconds", 0.02, map[string]string{"service": "api", "module": "http", "operation": "get", "result": "success"}); err != nil {
		t.Fatal(err)
	}
	app := httpapi.New(httpapi.Dependencies{
		Admin: admin.NewService("test", registry), Metrics: registry,
		Sentry: mailflowsentry.NewService(4), Translations: translations.NewCatalog(),
	})

	response, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/metrics?resolution=minute&limit=10", nil))
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("metrics response status=%d err=%v", response.StatusCode, err)
	}
	var payload struct {
		Items []metrics.SeriesPoint `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 1 || payload.Items[0].P95 == nil {
		t.Fatalf("unexpected metric payload: %+v", payload.Items)
	}

	response, err = app.Test(httptest.NewRequest("GET", "/api/v1/admin/metrics?resolution=raw", nil))
	if err != nil || response.StatusCode != 400 {
		t.Fatalf("invalid query status=%d err=%v", response.StatusCode, err)
	}
}
