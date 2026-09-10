package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

type manualQuotaClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *manualQuotaClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *manualQuotaClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.mu.Lock()
	clock.now = clock.now.Add(delay)
	clock.mu.Unlock()
	return nil
}

func TestQuotaLimiterSpacesWeightedRequests(t *testing.T) {
	start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	clock := &manualQuotaClock{now: start}
	limiter := newQuotaLimiter(60, clock)
	for _, cost := range []int{5, 20, 20, 1} {
		if err := limiter.Wait(context.Background(), cost); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := clock.Now().Sub(start); elapsed != 46*time.Second {
		t.Fatalf("elapsed = %s, want 46s", elapsed)
	}
}

func TestQuotaLimiterCancellationInterruptsPendingRequest(t *testing.T) {
	limiter := newQuotaLimiter(1, systemQuotaClock{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := limiter.Wait(ctx, 1)
	if err == nil || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("cancellation error=%v elapsed=%s", err, time.Since(started))
	}
}

func TestBackfillPageStaysWithinQuotaAndPacesAttachmentMetadata(t *testing.T) {
	const unitsPerMinute = 60
	start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	clock := &manualQuotaClock{now: start}
	type charge struct {
		at    time.Time
		units int
	}
	charges := make([]charge, 0, 102)
	maxWindow := 0

	attachmentMessage := "From: Sender <sender@example.test>\r\nTo: Owner <owner@example.test>\r\nSubject: Attachment fixture\r\nMessage-ID: <attachment@example.test>\r\nContent-Type: multipart/mixed; boundary=fixture\r\n\r\n--fixture\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nFixture body\r\n--fixture\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=fixture.txt\r\nContent-Transfer-Encoding: base64\r\n\r\nZml4dHVyZQ==\r\n--fixture--\r\n"

	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/", func(response http.ResponseWriter, request *http.Request) {
		path := request.URL.Path[len("/gmail/v1/users/me"):]
		units := quotaCost(request.Method, path)
		now := clock.Now()
		kept := charges[:0]
		window := units
		for _, item := range charges {
			if now.Sub(item.at) < time.Minute {
				kept = append(kept, item)
				window += item.units
			}
		}
		charges = append(kept, charge{at: now, units: units})
		if window > maxWindow {
			maxWindow = window
		}
		if window > unitsPerMinute {
			response.WriteHeader(http.StatusForbidden)
			_, _ = response.Write([]byte(`{"error":{"errors":[{"reason":"userRateLimitExceeded"}]}}`))
			return
		}
		response.Header().Set("Content-Type", "application/json")
		switch {
		case path == "/messages":
			messages := make([]map[string]string, 100)
			for index := range messages {
				messages[index] = map[string]string{"id": fmt.Sprintf("message-%03d", index)}
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"messages": messages})
		case request.URL.Query().Get("format") == "full":
			_ = json.NewEncoder(response).Encode(map[string]any{"payload": map[string]any{"parts": []any{map[string]any{"filename": "fixture.txt", "mimeType": "text/plain", "body": map[string]string{"attachmentId": "attachment-1"}}}}})
		case request.URL.Query().Get("format") == "raw":
			id := path[len("/messages/"):]
			raw := sanitizedMessage
			if id == "message-000" {
				raw = attachmentMessage
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"id": id, "threadId": "thread-" + id, "internalDate": "1788998400000", "labelIds": []string{"INBOX"}, "raw": base64.RawURLEncoding.EncodeToString([]byte(raw))})
		default:
			http.NotFound(response, request)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	normalizer, err := mail.NewNormalizer(mail.DefaultMIMEPolicy())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := newProvider("test-access", server.URL+"/gmail/v1/users/me", server.Client(), normalizer, newQuotaLimiter(unitsPerMinute, clock))
	if err != nil {
		t.Fatal(err)
	}
	before := start.Add(24 * time.Hour)
	page, err := provider.Backfill(context.Background(), mail.SyncCursor{}, nil, &before, 100)
	if err != nil || len(page.Messages) != 100 || maxWindow > unitsPerMinute {
		t.Fatalf("messages=%d max_window=%d error=%v", len(page.Messages), maxWindow, err)
	}
}

func TestIndependentQuotaLimitersDoNotShareReservations(t *testing.T) {
	start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	firstClock := &manualQuotaClock{now: start}
	secondClock := &manualQuotaClock{now: start}
	if err := newQuotaLimiter(60, firstClock).Wait(context.Background(), 20); err != nil {
		t.Fatal(err)
	}
	if err := newQuotaLimiter(60, secondClock).Wait(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if firstClock.Now().Sub(start) != 20*time.Second || secondClock.Now().Sub(start) != time.Second {
		t.Fatal("independent limiters shared a reservation")
	}
}

func TestQuotaCostsCoverEveryGmailProviderOperation(t *testing.T) {
	tests := map[string]int{
		"GET /profile": 1, "GET /labels": 1, "GET /history": 2, "GET /messages": 5,
		"GET /messages/id": 20, "GET /messages/id/attachments/part": 20,
		"POST /messages/send": 100, "POST /drafts/send": 100, "POST /drafts": 10,
		"PUT /drafts/id": 15, "DELETE /drafts/id": 10, "POST /messages/id/modify": 5,
		"POST /messages/id/trash": 20, "POST /messages/id/untrash": 5,
		"POST /threads/id/modify": 10, "POST /threads/id/trash": 20, "POST /threads/id/untrash": 10,
	}
	for request, want := range tests {
		var method, path string
		_, _ = fmt.Sscanf(request, "%s %s", &method, &path)
		if got := quotaCost(method, path); got != want {
			t.Fatalf("%s cost=%d want=%d", request, got, want)
		}
	}
}
