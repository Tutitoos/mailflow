package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/alerts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
)

type successfulAlertSender struct{}

func (successfulAlertSender) Send(context.Context, alerts.Message) error { return nil }

func TestAdminAlertTestAndStatus(t *testing.T) {
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
	service, err := alerts.NewService(pool, successfulAlertSender{}, nil, alerts.Config{Cooldown: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	app, token := authenticatedAdminApp(t, httpapi.Dependencies{Admin: admin.NewService("test", metrics.NewRegistry()), Alerts: service, Sentry: mailflowsentry.NewService(1024), Translations: translations.NewCatalog()})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/alerts/test", strings.NewReader(`{"confirmation":"send"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "alert-test-00000001")
	authorizeAdmin(request, token)
	response, err := app.Test(request)
	if err != nil || response.StatusCode != http.StatusAccepted {
		t.Fatalf("status=%d err=%v", response.StatusCode, err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/alerts", nil)
	authorizeAdmin(request, token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d err=%v", response.StatusCode, err)
	}
	var status alerts.Status
	if err = json.NewDecoder(response.Body).Decode(&status); err != nil || !status.Configured || len(status.Incidents) != 1 || len(status.Deliveries) != 1 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}
