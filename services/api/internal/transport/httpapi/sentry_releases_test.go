package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
)

type sentryReleaseQueue struct{}

func (sentryReleaseQueue) Enqueue(_ context.Context, kind string, payload json.RawMessage, options queue.EnqueueOptions) (queue.Job, bool, error) {
	return queue.Job{Version: 1, ID: "artifact-test", Kind: kind, Payload: payload, MaxAttempts: options.MaxAttempts}, true, nil
}

func TestSentryCLIRoutesAndAdminIssueContract(t *testing.T) {
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
	store, err := cdn.NewStore(t.TempDir(), 25<<20)
	if err != nil {
		t.Fatal(err)
	}
	service, err := mailflowsentry.NewPersistentService(pool, store, mailflowsentry.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	projects, err := mailflowsentry.DerivedProjects([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureProjects(ctx, projects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	service.SetQueue(sentryReleaseQueue{})
	registry := metrics.NewRegistry()
	app := httpapi.New(httpapi.Dependencies{Admin: admin.NewService("test", registry), Metrics: registry, Sentry: service, Translations: translations.NewCatalog()})

	releaseRequest := httptest.NewRequest(http.MethodPost, "/api/0/organizations/mailflow/releases/", bytes.NewBufferString(`{"version":"v1.2.3","projects":["web"]}`))
	releaseRequest.Header.Set("Content-Type", "application/json")
	releaseRequest.Header.Set("Authorization", "Bearer "+projects[1].ArtifactToken)
	releaseResponse, err := app.Test(releaseRequest)
	if err != nil || releaseResponse.StatusCode != http.StatusCreated {
		t.Fatalf("release status=%d err=%v", releaseResponse.StatusCode, err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("name", "bundle.js.map")
	file, err := writer.CreateFormFile("file", "bundle.js.map")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte(`{"version":3,"file":"bundle.js","sources":["app.ts"],"names":["renderInbox"],"mappings":"AAAAA"}`))
	_ = writer.Close()
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/0/projects/mailflow/web/releases/v1.2.3/files/", &body)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	uploadRequest.Header.Set("Authorization", "Bearer "+projects[1].ArtifactToken)
	uploadResponse, err := app.Test(uploadRequest)
	if err != nil || uploadResponse.StatusCode != http.StatusCreated {
		t.Fatalf("upload status=%d err=%v", uploadResponse.StatusCode, err)
	}

	eventID := "abababababababababababababababab"
	payload := []byte(`{"event_id":"` + eventID + `","exception":{"values":[{"type":"RouteError","stacktrace":{"frames":[{"filename":"bundle.js","lineno":1}]}}]}}`)
	envelope := []byte(fmt.Sprintf("{\"event_id\":%q}\n{\"type\":\"event\",\"length\":%d}\n%s", eventID, len(payload), payload))
	if _, err := service.Ingest(ctx, mailflowsentry.Request{QueryKey: projects[1].PublicKey, Body: envelope}); err != nil {
		t.Fatal(err)
	}
	issuesResponse, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/admin/sentry?status=unresolved&limit=10", nil))
	if err != nil || issuesResponse.StatusCode != http.StatusOK {
		t.Fatalf("issues status=%d err=%v", issuesResponse.StatusCode, err)
	}
	var issues struct {
		Items []mailflowsentry.Issue `json:"items"`
	}
	if json.NewDecoder(issuesResponse.Body).Decode(&issues) != nil || len(issues.Items) != 1 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	statusRequest := httptest.NewRequest(http.MethodPut, "/api/v1/admin/sentry/"+issues.Items[0].ID, bytes.NewBufferString(`{"status":"resolved"}`))
	statusRequest.Header.Set("Content-Type", "application/json")
	statusResponse, err := app.Test(statusRequest)
	if err != nil || statusResponse.StatusCode != http.StatusOK {
		t.Fatalf("status update=%d err=%v", statusResponse.StatusCode, err)
	}
}
