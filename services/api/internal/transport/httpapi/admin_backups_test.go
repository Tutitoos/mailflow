package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/backups"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
)

func TestAdminBackupsReturnsBoundedRedactedState(t *testing.T) {
	ctx := context.Background()
	databaseURL := testkit.PostgresDatabase(t)
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repository := backups.NewRepository(pool)
	now := time.Now().UTC()
	if err := repository.UpdateRuntime(ctx, backups.Runtime{Enabled: true, RepositoryKind: "local", Schedule: "03:00", Timezone: "UTC", NextRunAt: now.Add(time.Hour), HeartbeatAt: now}); err != nil {
		t.Fatal(err)
	}
	app, token := authenticatedAdminApp(t, httpapi.Dependencies{
		Admin: admin.NewService("test", metrics.NewRegistry()), Backups: repository,
		Sentry: mailflowsentry.NewService(1024), Translations: translations.NewCatalog(),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backups?limit=1", nil)
	authorizeAdmin(request, token)
	response, err := app.Test(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d err=%v", response.StatusCode, err)
	}
	var status backups.Status
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil || !status.Configured || status.Runtime == nil || len(status.Runs) != 0 {
		t.Fatalf("status=%+v err=%v", status, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/backups?limit=101", nil)
	authorizeAdmin(request, token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid limit status=%d err=%v", response.StatusCode, err)
	}
}
