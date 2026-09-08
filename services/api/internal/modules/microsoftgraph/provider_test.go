package microsoftgraph

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

type recordedRequest struct {
	Method      string
	URI         string
	Prefer      string
	ContentType string
	Body        string
}

type graphFixtureState struct {
	mu       sync.Mutex
	requests []recordedRequest
	drafts   int
}

func graphFixture(t *testing.T) (*Provider, *httptest.Server, *graphFixtureState) {
	t.Helper()
	state := &graphFixtureState{}
	consumer := fixture(t, "consumer_messages.json")
	m365 := fixture(t, "microsoft_365_messages.json")
	mimeMessage := fixture(t, "message.eml")
	plainMessage := []byte("From: sender@example.test\r\nTo: owner@example.test\r\nSubject: Microsoft 365 fixture\r\nMessage-ID: <m365@example.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nSanitized body")
	mux := http.NewServeMux()
	handler := func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		state.mu.Lock()
		state.requests = append(state.requests, recordedRequest{Method: request.Method, URI: request.URL.RequestURI(), Prefer: request.Header.Get("Prefer"), ContentType: request.Header.Get("Content-Type"), Body: string(body)})
		state.mu.Unlock()
		if request.Header.Get("Authorization") != "Bearer test-access" || request.Header.Get("Prefer") != `IdType="ImmutableId"` {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(request.URL.Path, "/v1.0/me")
		switch {
		case path == "" && request.Method == http.MethodGet:
			writeFixtureJSON(response, map[string]string{"id": "graph-user-id", "mail": "", "userPrincipalName": "owner@example.test"})
		case path == "/mailFolders" && request.URL.Query().Get("$skiptoken") == "":
			writeFixtureJSON(response, map[string]any{
				"@odata.nextLink": serverLink(request, "/v1.0/me/mailFolders?%24skiptoken=folders-2"),
				"value": []any{
					map[string]any{"id": "inbox-folder-id", "displayName": "Inbox", "totalItemCount": 8, "unreadItemCount": 2},
					map[string]any{"id": "custom-folder-id", "displayName": "Projects", "totalItemCount": 3, "unreadItemCount": 1},
				},
			})
		case path == "/mailFolders" && request.URL.Query().Get("$skiptoken") == "folders-2":
			writeFixtureJSON(response, map[string]any{"value": []any{map[string]any{"id": "archive-folder-id", "displayName": "Archive", "totalItemCount": 4, "unreadItemCount": 0}}})
		case strings.HasPrefix(path, "/mailFolders/") && request.Method == http.MethodGet:
			wellKnown := strings.TrimPrefix(path, "/mailFolders/")
			ids := map[string]string{
				"inbox": "inbox-folder-id", "sentitems": "sent-folder-id", "drafts": "drafts-folder-id",
				"deleteditems": "deleted-folder-id", "junkemail": "junk-folder-id", "archive": "archive-folder-id",
			}
			if id := ids[wellKnown]; id != "" {
				writeFixtureJSON(response, map[string]string{"id": id})
				return
			}
			http.NotFound(response, request)
		case path == "/outlook/masterCategories":
			writeFixtureJSON(response, map[string]any{"value": []any{
				map[string]string{"id": "category-1", "displayName": "Receipts", "color": "preset0"},
				map[string]string{"id": "category-2", "displayName": "Projects", "color": "preset1"},
			}})
		case path == "/messages" && request.URL.Query().Get("$skiptoken") == "thread-page-2":
			writeFixtureJSON(response, map[string]any{"value": []any{map[string]string{"id": "thread-message-2"}}})
		case path == "/messages" && strings.Contains(request.URL.Query().Get("$filter"), "conversationId"):
			writeFixtureJSON(response, map[string]any{"@odata.nextLink": serverLink(request, "/v1.0/me/messages?%24skiptoken=thread-page-2"), "value": []any{map[string]string{"id": "thread-message-1"}}})
		case path == "/messages" && request.URL.Query().Get("$skiptoken") == "consumer-page-2":
			_, _ = response.Write(m365)
		case path == "/messages" && request.Method == http.MethodGet:
			base := requestBase(request) + "/v1.0/me"
			_, _ = response.Write([]byte(strings.ReplaceAll(string(consumer), "{{BASE_URL}}", base)))
		case strings.HasSuffix(path, "/$value") && !strings.Contains(path, "/attachments/"):
			response.Header().Set("Content-Type", "message/rfc822")
			if strings.Contains(path, "m365-message-2") {
				_, _ = response.Write(plainMessage)
			} else {
				_, _ = response.Write(mimeMessage)
			}
		case strings.HasSuffix(path, "/attachments"):
			writeFixtureJSON(response, map[string]any{"value": []any{map[string]any{"id": "attachment-1", "name": "fixture.txt", "contentType": "text/plain", "size": 18, "isInline": false}}})
		case strings.HasSuffix(path, "/attachments/attachment-1/$value"):
			response.Header().Set("Content-Type", "text/plain")
			_, _ = response.Write([]byte("Fixture attachment"))
		case path == "/messages" && request.Method == http.MethodPost:
			decoded, err := base64.StdEncoding.DecodeString(string(body))
			if err != nil || !strings.Contains(string(decoded), "Sanitized Graph fixture") {
				http.Error(response, "invalid MIME", http.StatusBadRequest)
				return
			}
			state.mu.Lock()
			state.drafts++
			id := "draft-" + strconv.Itoa(state.drafts)
			state.mu.Unlock()
			response.WriteHeader(http.StatusCreated)
			writeFixtureJSON(response, map[string]string{"id": id})
		case strings.HasSuffix(path, "/send") && request.Method == http.MethodPost:
			response.WriteHeader(http.StatusAccepted)
		case request.Method == http.MethodDelete:
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodGet && strings.HasPrefix(path, "/messages/"):
			writeFixtureJSON(response, map[string]any{"categories": []string{"Existing", "Receipts"}})
		case request.Method == http.MethodPatch:
			writeFixtureJSON(response, map[string]string{"id": strings.TrimPrefix(path, "/messages/")})
		case strings.HasSuffix(path, "/move") && request.Method == http.MethodPost:
			response.WriteHeader(http.StatusCreated)
			writeFixtureJSON(response, map[string]string{"id": strings.TrimSuffix(strings.TrimPrefix(path, "/messages/"), "/move")})
		default:
			http.NotFound(response, request)
		}
	}
	mux.HandleFunc("/v1.0/me", handler)
	mux.HandleFunc("/v1.0/me/", handler)
	server := httptest.NewServer(mux)
	provider := newGraphFixtureProvider(t, server)
	return provider, server, state
}

func newGraphFixtureProvider(t *testing.T, server *httptest.Server) *Provider {
	t.Helper()
	normalizer, err := mail.NewNormalizer(mail.DefaultMIMEPolicy())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewWithBaseURL("test-access", server.URL+"/v1.0/me", server.Client(), normalizer)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestProviderMapsConsumerAndMicrosoft365Contracts(t *testing.T) {
	provider, server, state := graphFixture(t)
	defer server.Close()
	ctx := context.Background()
	profile, err := provider.Profile(ctx)
	if err != nil || profile.RemoteID != "graph-user-id" || profile.Address != "owner@example.test" || profile.History.Kind != "microsoft_delta" {
		t.Fatalf("profile = %+v, %v", profile, err)
	}
	capabilities, err := provider.Capabilities(ctx)
	if err != nil || !capabilities["categories"] || !capabilities["attachments"] || !capabilities["send"] {
		t.Fatalf("capabilities = %+v, %v", capabilities, err)
	}
	firstCatalog, err := provider.Catalog(ctx, mail.SyncCursor{})
	if err != nil || !firstCatalog.HasMore || len(firstCatalog.Mailboxes) != 2 || firstCatalog.Mailboxes[0].Role != mail.MailboxInbox || firstCatalog.Mailboxes[1].Role != "" || len(firstCatalog.Labels) != 2 || firstCatalog.Labels[0].RemoteID != "Receipts" {
		t.Fatalf("first catalog = %+v, %v", firstCatalog, err)
	}
	secondCatalog, err := newGraphFixtureProvider(t, server).Catalog(ctx, firstCatalog.NextCursor)
	if err != nil || secondCatalog.HasMore || len(secondCatalog.Mailboxes) != 1 || secondCatalog.Mailboxes[0].Role != mail.MailboxArchive || len(secondCatalog.Labels) != 0 {
		t.Fatalf("second catalog = %+v, %v", secondCatalog, err)
	}
	after := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	firstPage, err := provider.Backfill(ctx, mail.SyncCursor{}, &after, &before, 50)
	if err != nil || !firstPage.HasMore || len(firstPage.Messages) != 1 {
		t.Fatalf("first page = %+v, %v", firstPage, err)
	}
	consumer := firstPage.Messages[0]
	if consumer.RemoteID != "consumer-message-1" || consumer.ThreadID != "consumer-conversation-1" || consumer.IsRead || !consumer.IsStarred || !consumer.IsImportant || !consumer.InTrash || consumer.Category != mail.CategoryPrimary || len(consumer.LabelIDs) != 1 || consumer.Content.Subject != "Sanitized Graph fixture" || strings.Contains(consumer.Content.BodyHTML, "script") || len(consumer.Content.Attachments) != 1 || consumer.Content.Attachments[0].RemoteID != "attachment-1" {
		t.Fatalf("consumer message = %+v", consumer)
	}
	secondPage, err := newGraphFixtureProvider(t, server).Backfill(ctx, firstPage.NextCursor, &after, &before, 50)
	if err != nil || secondPage.HasMore || len(secondPage.Messages) != 1 || secondPage.Messages[0].RemoteID != "m365-message-2" || secondPage.Messages[0].ThreadID != "m365-conversation-2" || secondPage.Messages[0].InTrash {
		t.Fatalf("m365 page = %+v, %v", secondPage, err)
	}
	if !requestsContain(state.snapshot(), "%24filter=receivedDateTime+ge+2026-09-01T00%3A00%3A00Z+and+receivedDateTime+lt+2026-09-09T00%3A00%3A00Z") {
		t.Fatalf("backfill filter missing: %+v", state.snapshot())
	}
	if countRequests(state.snapshot(), http.MethodGet, "/mailFolders/") != len(standardFolders) {
		t.Fatalf("well-known folder IDs were fetched again across page cursors: %+v", state.snapshot())
	}
	for _, request := range state.snapshot() {
		if request.Prefer != `IdType="ImmutableId"` {
			t.Fatalf("request omitted immutable ID preference: %+v", request)
		}
	}
}

func TestProviderAppliesThreadActionsWithoutReplacingCategories(t *testing.T) {
	provider, server, state := graphFixture(t)
	defer server.Close()
	ctx := context.Background()
	if err := provider.Apply(ctx, mail.RemoteAction{Kind: "mark_read", TargetKind: "thread", TargetIDs: []string{"conversation-'fixture"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Apply(ctx, mail.RemoteAction{Kind: "add_label", TargetKind: "message", TargetIDs: []string{"consumer-message-1"}, LabelIDs: []string{"Projects", "receipts"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Apply(ctx, mail.RemoteAction{Kind: "move_to_trash", TargetKind: "message", TargetIDs: []string{"consumer-message-1"}}); err != nil {
		t.Fatal(err)
	}
	requests := state.snapshot()
	if !requestsContain(requests, "conversationId+eq+%27conversation-%27%27fixture%27") || countRequests(requests, http.MethodPatch, "/messages/thread-message-") != 2 {
		t.Fatalf("thread action requests = %+v", requests)
	}
	categoryPatch := findRequest(requests, http.MethodPatch, "/messages/consumer-message-1")
	if !strings.Contains(categoryPatch.Body, `"categories":["Existing","Projects","Receipts"]`) {
		t.Fatalf("category patch = %s", categoryPatch.Body)
	}
	move := findRequest(requests, http.MethodPost, "/messages/consumer-message-1/move")
	if move.Body != `{"destinationId":"deleteditems"}` {
		t.Fatalf("move body = %s", move.Body)
	}
}

func TestProviderCreatesReplacesSendsAndDownloads(t *testing.T) {
	provider, server, state := graphFixture(t)
	defer server.Close()
	payload := string(fixture(t, "message.eml"))
	draftID, err := provider.SaveDraft(context.Background(), mail.OutgoingMessage{DraftID: "old-draft", Raw: strings.NewReader(payload)})
	if err != nil || draftID != "draft-1" {
		t.Fatalf("draft = %q, %v", draftID, err)
	}
	sentID, err := provider.Send(context.Background(), mail.OutgoingMessage{Raw: strings.NewReader(payload)})
	if err != nil || sentID != "draft-2" {
		t.Fatalf("send = %q, %v", sentID, err)
	}
	attachment, err := provider.DownloadAttachment(context.Background(), "consumer-message-1", "attachment-1")
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(attachment)
	closeErr := attachment.Close()
	if readErr != nil || closeErr != nil || string(content) != "Fixture attachment" {
		t.Fatalf("attachment = %q, %v, %v", content, readErr, closeErr)
	}
	requests := state.snapshot()
	if findRequest(requests, http.MethodPost, "/messages").ContentType != "text/plain" || findRequest(requests, http.MethodDelete, "/messages/old-draft").Method == "" || findRequest(requests, http.MethodPost, "/messages/draft-2/send").Method == "" {
		t.Fatalf("draft/send requests = %+v", requests)
	}
}

func TestProviderRejectsCrossOriginPagingAndClassifiesSanitizedFailures(t *testing.T) {
	provider, server, _ := graphFixture(t)
	defer server.Close()
	if _, err := provider.relativeNextLink("https://attacker.example/v1.0/me/messages?token=private"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-origin nextLink error = %v", err)
	}
	normalizer, _ := mail.NewNormalizer(mail.DefaultMIMEPolicy())
	if _, err := NewWithBaseURL("private-token", "http://attacker.example/v1.0/me", nil, normalizer); err == nil {
		t.Fatal("insecure non-loopback Graph endpoint was accepted")
	}
	if _, err := provider.Backfill(context.Background(), mail.SyncCursor{Kind: "google_backfill", Value: []byte(`{"next":"private"}`)}, timePointer(time.Now().Add(-time.Hour)), nil, 10); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-provider cursor error = %v", err)
	}
	if _, err := provider.Backfill(context.Background(), mail.SyncCursor{Kind: "microsoft_backfill", Value: []byte(`{"next":"/messages?%24skiptoken=forged"}`)}, timePointer(time.Now().Add(-time.Hour)), nil, 10); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("unscoped continuation cursor error = %v", err)
	}
	payload := fixture(t, "throttled.json")
	err := classify(http.StatusTooManyRequests, "7", payload)
	var providerError *ProviderError
	if !errors.As(err, &providerError) || providerError.Kind != ErrorQuota || providerError.StatusCode != http.StatusTooManyRequests || providerError.Code != "errorquotaexceeded" || providerError.RetryAfter != 7*time.Second || strings.Contains(err.Error(), "Sanitized") {
		t.Fatalf("provider error = %+v", err)
	}
	for _, test := range []struct {
		status int
		kind   ErrorKind
	}{{http.StatusUnauthorized, ErrorAuthorization}, {http.StatusForbidden, ErrorAuthorization}, {http.StatusRequestTimeout, ErrorTransient}, {http.StatusServiceUnavailable, ErrorTransient}, {http.StatusNotFound, ErrorPermanent}} {
		var typed *ProviderError
		if err := classify(test.status, "", nil); !errors.As(err, &typed) || typed.Kind != test.kind {
			t.Fatalf("status %d = %+v", test.status, err)
		}
	}
}

func (state *graphFixtureState) snapshot() []recordedRequest {
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]recordedRequest(nil), state.requests...)
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	value, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func writeFixtureJSON(response http.ResponseWriter, value any) {
	_ = json.NewEncoder(response).Encode(value)
}

func requestBase(request *http.Request) string { return "http://" + request.Host }

func serverLink(request *http.Request, path string) string { return requestBase(request) + path }

func requestsContain(requests []recordedRequest, fragment string) bool {
	for _, request := range requests {
		if strings.Contains(request.URI, fragment) {
			return true
		}
	}
	return false
}

func countRequests(requests []recordedRequest, method, fragment string) int {
	count := 0
	for _, request := range requests {
		if request.Method == method && strings.Contains(request.URI, fragment) {
			count++
		}
	}
	return count
}

func findRequest(requests []recordedRequest, method, fragment string) recordedRequest {
	for _, request := range requests {
		if request.Method == method && strings.Contains(request.URI, fragment) {
			return request
		}
	}
	return recordedRequest{}
}

func timePointer(value time.Time) *time.Time { return &value }
