package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/logs"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
)

func TestAdminLogsFilterAndControlTemporaryDebug(t *testing.T) {
	ctx := context.Background()
	databaseURL := testkit.PostgresDatabase(t)
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, err := logs.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.WriteBatch(ctx, []logs.Entry{{OccurredAt: now, Service: "api", Module: "http", Level: "warning", Event: "http.slow", RequestID: "request-safe", Attributes: map[string]any{"subject": "private"}}}); err != nil {
		t.Fatal(err)
	}
	pipeline, err := logs.NewPipeline(io.Discard, "api", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Attach(store)
	registry := metrics.NewRegistry()
	app, token := authenticatedAdminApp(t, httpapi.Dependencies{Admin: admin.NewService("test", registry), Logs: pipeline, Metrics: registry, Sentry: mailflowsentry.NewService(4), Translations: translations.NewCatalog()})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/logs?service=api&module=http&level=warning&event=http.slow&requestId=request-safe&limit=10", nil)
	authorizeAdmin(request, token)
	response, err := app.Test(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("logs status=%d err=%v", response.StatusCode, err)
	}
	var page struct {
		Items   []logs.Entry `json:"items"`
		Dropped uint64       `json:"dropped"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Attributes["subject"] != "[REDACTED]" {
		t.Fatalf("unsafe page: %+v", page)
	}

	request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/logs/debug", bytes.NewBufferString(`{"durationSeconds":60}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeAdmin(request, token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("debug status=%d err=%v", response.StatusCode, err)
	}
	var status struct {
		Enabled      bool       `json:"enabled"`
		EnabledUntil *time.Time `json:"enabledUntil"`
	}
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.EnabledUntil == nil {
		t.Fatalf("debug status=%+v", status)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/logs?service=owner@example.test", nil)
	authorizeAdmin(request, token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsafe filter status=%d err=%v", response.StatusCode, err)
	}
}
