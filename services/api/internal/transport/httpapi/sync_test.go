package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/gofiber/fiber/v3"
)

type fakeSyncRequester struct{ err error }

func (requester fakeSyncRequester) Request(context.Context, string, string) (mailflowsync.Run, error) {
	return mailflowsync.Run{ID: "0199ed3b-c950-7000-8000-000000000020"}, requester.err
}
func (requester fakeSyncRequester) StartInitial(context.Context, string, string) (mailflowsync.Run, error) {
	return mailflowsync.Run{ID: "0199ed3b-c950-7000-8000-000000000021"}, requester.err
}

func TestSynchronizeAccountRequiresIdempotencyAndQueuesOwnerScopedRun(t *testing.T) {
	requester := fakeSyncRequester{}
	app := fiber.New(fiber.Config{ErrorHandler: problemHandler})
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(authbridge.WithUser(c.Context(), authbridge.User{ID: "0199ed3b-c950-7000-8000-000000000001"}))
		return c.Next()
	})
	app.Post("/accounts/:accountId/sync", synchronizeAccount(requester))
	missing, err := app.Test(httptest.NewRequest(http.MethodPost, "/accounts/0199ed3b-c950-7000-8000-000000000016/sync", nil))
	if err != nil || missing.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing key status=%d error=%v", missing.StatusCode, err)
	}
	request := httptest.NewRequest(http.MethodPost, "/accounts/0199ed3b-c950-7000-8000-000000000016/sync", nil)
	request.Header.Set("Idempotency-Key", "manual-sync-key-01")
	response, err := app.Test(request)
	if err != nil || response.StatusCode != http.StatusAccepted {
		t.Fatalf("sync status=%d error=%v", response.StatusCode, err)
	}

	app = fiber.New(fiber.Config{ErrorHandler: problemHandler})
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(authbridge.WithUser(c.Context(), authbridge.User{ID: "0199ed3b-c950-7000-8000-000000000001"}))
		return c.Next()
	})
	app.Post("/accounts/:accountId/sync", synchronizeAccount(fakeSyncRequester{err: mailflowsync.ErrRunExists}))
	request = httptest.NewRequest(http.MethodPost, "/accounts/0199ed3b-c950-7000-8000-000000000016/sync", nil)
	request.Header.Set("Idempotency-Key", "manual-sync-key-02")
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusAccepted {
		t.Fatalf("duplicate sync status=%d error=%v", response.StatusCode, err)
	}
}
