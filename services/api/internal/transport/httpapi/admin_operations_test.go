package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
)

type adminQueue struct{ stats queue.Stats }

func (store adminQueue) Ping(context.Context) error { return nil }
func (store adminQueue) Stats(context.Context) (queue.Stats, error) {
	return store.stats, nil
}

type adminSynchronizer struct{ calls int }

func (scheduler *adminSynchronizer) Request(_ context.Context, _, accountID string) (mailflowsync.Run, error) {
	scheduler.calls++
	return mailflowsync.Run{ID: "00000000-0000-7000-8000-000000000055", AccountID: accountID, State: mailflowsync.RunQueued}, nil
}
func (scheduler *adminSynchronizer) StartInitial(_ context.Context, _, accountID string) (mailflowsync.Run, error) {
	return scheduler.Request(context.Background(), "", accountID)
}

func TestAdminQueueControlsAreConfirmedIdempotentAndPrivate(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `insert into users (id,email,name,locale) values ($1,'owner@example.test','owner','en')`, testUserID); err != nil {
		t.Fatal(err)
	}
	queueStore := adminQueue{stats: queue.Stats{Ready: 2, Retry: 1}}
	service := admin.NewServiceWithMetrics("test", metrics.NewService(metrics.NewRegistry(), pool), admin.Options{
		DatabaseProbe: pool.Ping, Queue: queueStore, Pool: pool,
	})
	synchronizer := &adminSynchronizer{}
	app, token := authenticatedAdminApp(t, httpapi.Dependencies{
		Admin: service, Metrics: metrics.NewRegistry(), Sync: synchronizer,
		Sentry: mailflowsentry.NewService(1024), Translations: translations.NewCatalog(),
	})

	accountID := "00000000-0000-7000-8000-000000000056"
	call := func(key, confirmation string) (*http.Response, string) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/queue/retry", bytes.NewBufferString(`{"accountId":"`+accountID+`","confirmation":"`+confirmation+`"}`))
		request.Header.Set("Content-Type", "application/json")
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		authorizeAdmin(request, token)
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		return response, string(body)
	}

	response, _ := call("", "retry")
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing idempotency status=%d", response.StatusCode)
	}
	response, body := call("stable-admin-action", "retry")
	if response.StatusCode != http.StatusAccepted || synchronizer.calls != 1 || strings.Contains(body, accountID) || strings.Contains(body, "stable-admin-action") {
		t.Fatalf("first status=%d calls=%d body=%s", response.StatusCode, synchronizer.calls, body)
	}
	response, body = call("stable-admin-action", "retry")
	if response.StatusCode != http.StatusOK || synchronizer.calls != 1 || !strings.Contains(body, `"created":false`) {
		t.Fatalf("duplicate status=%d calls=%d body=%s", response.StatusCode, synchronizer.calls, body)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/queue", nil)
	authorizeAdmin(request, token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("overview status=%d err=%v", response.StatusCode, err)
	}
	var overview admin.QueueOverview
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil || overview.Stats.Retry != 1 || len(overview.Operations) != 1 {
		t.Fatalf("overview=%+v err=%v", overview, err)
	}
}
