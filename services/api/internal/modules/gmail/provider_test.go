package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

const sanitizedMessage = "From: Sender <sender@example.test>\r\nTo: Owner <owner@example.test>\r\nSubject: Sanitized fixture\r\nMessage-ID: <fixture@example.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nFixture body"

func gmailFixture(t *testing.T) (*Provider, *httptest.Server, *[]string) {
	t.Helper()
	requests := &[]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/", func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-access" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		*requests = append(*requests, request.Method+" "+request.URL.RequestURI())
		response.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(request.URL.Path, "/gmail/v1/users/me")
		switch {
		case path == "/profile":
			writeJSON(response, map[string]string{"emailAddress": "owner@example.test", "historyId": "100"})
		case path == "/labels":
			writeJSON(response, map[string]any{"labels": []map[string]any{
				{"id": "INBOX", "name": "INBOX", "type": "system", "messagesTotal": 8, "messagesUnread": 2},
				{"id": "CATEGORY_PROMOTIONS", "name": "CATEGORY_PROMOTIONS", "type": "system", "messagesTotal": 3},
				{"id": "Label_1", "name": "Projects", "type": "user", "messagesTotal": 4},
			}})
		case path == "/history" && request.URL.Query().Get("pageToken") == "":
			writeJSON(response, map[string]any{"history": []any{map[string]any{"messagesAdded": []any{map[string]any{"message": map[string]string{"id": "message-1"}}}, "messagesDeleted": []any{map[string]any{"message": map[string]string{"id": "message-deleted"}}}}}, "historyId": "101", "nextPageToken": "history-page-2"})
		case path == "/history":
			writeJSON(response, map[string]any{"history": []any{}, "historyId": "102"})
		case path == "/messages" && request.Method == http.MethodGet:
			writeJSON(response, map[string]any{"messages": []any{map[string]string{"id": "message-1"}}, "nextPageToken": "backfill-page-2"})
		case path == "/messages/message-1" && request.Method == http.MethodGet:
			writeJSON(response, map[string]any{"id": "message-1", "threadId": "thread-1", "internalDate": "1788782400000", "labelIds": []string{"UNREAD", "STARRED", "CATEGORY_PROMOTIONS"}, "raw": base64.URLEncoding.EncodeToString([]byte(sanitizedMessage))})
		case strings.HasSuffix(path, "/modify"):
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write([]byte(`{}`))
		case path == "/drafts":
			writeJSON(response, map[string]string{"id": "draft-1"})
		case path == "/drafts/missing-draft" && request.Method == http.MethodDelete:
			http.NotFound(response, request)
		case strings.HasPrefix(path, "/drafts/") && request.Method == http.MethodDelete:
			response.WriteHeader(http.StatusNoContent)
		case path == "/messages/send":
			writeJSON(response, map[string]string{"id": "sent-1"})
		case path == "/drafts/send":
			writeJSON(response, map[string]string{"id": "sent-draft-1"})
		case path == "/messages/message-1/attachments/attachment-1":
			writeJSON(response, map[string]string{"data": base64.URLEncoding.EncodeToString([]byte("attachment fixture"))})
		default:
			http.NotFound(response, request)
		}
	})
	server := httptest.NewServer(mux)
	normalizer, err := mail.NewNormalizer(mail.DefaultMIMEPolicy())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := newProvider("test-access", server.URL+"/gmail/v1/users/me", server.Client(), normalizer, immediateQuotaLimiter{})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return provider, server, requests
}

type immediateQuotaLimiter struct{}

func (immediateQuotaLimiter) Wait(context.Context, int) error { return nil }

type failingNormalizer struct{ err error }

func (normalizer failingNormalizer) Normalize(io.Reader) (mail.NormalizedMessageContent, error) {
	return mail.NormalizedMessageContent{}, normalizer.err
}

func TestProviderMapsProfileLabelsAndPaginatedChanges(t *testing.T) {
	provider, server, _ := gmailFixture(t)
	defer server.Close()
	ctx := context.Background()
	profile, err := provider.Profile(ctx)
	if err != nil || profile.Address != "owner@example.test" || profile.History.Kind != "google_history" {
		t.Fatalf("profile = %+v, %v", profile, err)
	}
	catalog, err := provider.Catalog(ctx, mail.SyncCursor{})
	if err != nil || len(catalog.Mailboxes) != 1 || catalog.Mailboxes[0].Role != mail.MailboxInbox || len(catalog.Labels) != 2 || catalog.Labels[0].Category == nil || *catalog.Labels[0].Category != mail.CategoryPromotions || catalog.Labels[1].Kind != mail.LabelUser {
		t.Fatalf("catalog = %+v, %v", catalog, err)
	}
	first, err := provider.Changes(ctx, profile.History)
	if err != nil || !first.HasMore || len(first.Messages) != 1 || len(first.DeletedRemoteIDs) != 1 || first.Messages[0].ThreadID != "thread-1" || first.Messages[0].Content.Subject != "Sanitized fixture" || first.Messages[0].IsRead || !first.Messages[0].IsStarred || first.Messages[0].Category != mail.CategoryPromotions {
		t.Fatalf("first changes = %+v, %v", first, err)
	}
	second, err := provider.Changes(ctx, first.NextCursor)
	if err != nil || second.HasMore || len(second.Messages) != 0 {
		t.Fatalf("second changes = %+v, %v", second, err)
	}
}

func TestProviderBackfillActionsDraftSendAndAttachment(t *testing.T) {
	provider, server, requests := gmailFixture(t)
	defer server.Close()
	ctx := context.Background()
	before := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	page, err := provider.Backfill(ctx, mail.SyncCursor{}, nil, &before, 25)
	if err != nil || !page.HasMore || string(page.NextCursor.Value) != "backfill-page-2" || len(page.Messages) != 1 {
		t.Fatalf("backfill = %+v, %v", page, err)
	}
	if err := provider.Apply(ctx, mail.RemoteAction{Kind: "mark_read", TargetKind: "thread", TargetIDs: []string{"thread-1"}}); err != nil {
		t.Fatal(err)
	}
	draftID, err := provider.SaveDraft(ctx, mail.OutgoingMessage{Raw: strings.NewReader(sanitizedMessage)})
	if err != nil || draftID != "draft-1" {
		t.Fatalf("draft = %q, %v", draftID, err)
	}
	sentID, err := provider.Send(ctx, mail.OutgoingMessage{Raw: strings.NewReader(sanitizedMessage)})
	if err != nil || sentID != "sent-1" {
		t.Fatalf("send = %q, %v", sentID, err)
	}
	sentDraftID, err := provider.Send(ctx, mail.OutgoingMessage{DraftID: draftID, Raw: strings.NewReader(sanitizedMessage)})
	if err != nil || sentDraftID != "sent-draft-1" {
		t.Fatalf("send draft = %q, %v", sentDraftID, err)
	}
	if err := provider.DeleteDraft(ctx, "draft-1"); err != nil {
		t.Fatalf("delete draft = %v", err)
	}
	if err := provider.DeleteDraft(ctx, "missing-draft"); err != nil {
		t.Fatalf("idempotent missing draft delete = %v", err)
	}
	attachment, err := provider.DownloadAttachment(ctx, "message-1", "attachment-1")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(attachment)
	_ = attachment.Close()
	if string(payload) != "attachment fixture" {
		t.Fatalf("attachment = %q", payload)
	}
	joined := strings.Join(*requests, "\n")
	if !strings.Contains(joined, "before%3A1788825600") || !strings.Contains(joined, "/threads/thread-1/modify") || !strings.Contains(joined, "DELETE /gmail/v1/users/me/drafts/draft-1") {
		t.Fatalf("unexpected requests:\n%s", joined)
	}
}

func TestProviderMapsAndDownloadsEmbeddedAttachmentPartsFromRaw(t *testing.T) {
	raw := strings.Join([]string{
		"From: Sender <sender@example.test>",
		"To: Owner <owner@example.test>",
		"Subject: Embedded attachment fixture",
		"Message-ID: <embedded@example.test>",
		"Content-Type: multipart/mixed; boundary=fixture",
		"",
		"--fixture",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Fixture body",
		"--fixture",
		"Content-Type: application/pdf",
		"Content-Disposition: attachment; filename=one.pdf",
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString([]byte("one")),
		"--fixture",
		"Content-Type: image/png",
		"Content-Disposition: inline",
		"Content-ID: <fixture-image@example.test>",
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString([]byte("two")),
		"--fixture--",
		"",
	}, "\r\n")
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages/message-1", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Query().Get("format") == "raw" {
			writeJSON(response, map[string]string{"raw": base64.RawURLEncoding.EncodeToString([]byte(raw))})
			return
		}
		writeJSON(response, map[string]any{"payload": map[string]any{"mimeType": "multipart/mixed", "parts": []any{
			map[string]any{"mimeType": "text/plain", "body": map[string]string{"data": "safe-inline-body"}},
			map[string]any{"filename": "one.pdf", "mimeType": "application/pdf", "body": map[string]string{"attachmentId": "attachment-1"}},
			map[string]any{"mimeType": "image/png", "body": map[string]string{"data": "safe-inline-image"}},
		}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	normalizer, _ := mail.NewNormalizer(mail.DefaultMIMEPolicy())
	provider, err := newProvider("test-access", server.URL+"/gmail/v1/users/me", server.Client(), normalizer, immediateQuotaLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	content, err := normalizer.Normalize(strings.NewReader(raw))
	if err != nil || len(content.Attachments) != 2 {
		t.Fatalf("normalized attachments=%d error=%v", len(content.Attachments), err)
	}
	if err := provider.mapAttachmentIDs(context.Background(), "message-1", &content); err != nil {
		t.Fatal(err)
	}
	if content.Attachments[0].RemoteID != "attachment-1" || content.Attachments[1].RemoteID != rawAttachmentPrefix+"1" {
		t.Fatalf("attachment references=%q,%q", content.Attachments[0].RemoteID, content.Attachments[1].RemoteID)
	}
	attachment, err := provider.DownloadAttachment(context.Background(), "message-1", content.Attachments[1].RemoteID)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(attachment)
	_ = attachment.Close()
	if string(payload) != "two" {
		t.Fatalf("embedded attachment bytes=%d", len(payload))
	}
}

func TestRawAttachmentReferenceRejectsForgedIndexes(t *testing.T) {
	for _, value := range []string{"", "-1", "01", "1024", "invalid"} {
		if _, ok := rawAttachmentIndex(rawAttachmentPrefix + value); ok {
			t.Fatalf("forged raw attachment index accepted: %q", value)
		}
	}
}

func TestGmailAttachmentCandidateMatchesBoundedMIMEKinds(t *testing.T) {
	for _, test := range []struct {
		filename      string
		mediaType     string
		attachmentID  string
		disposition   bool
		wantCandidate bool
	}{
		{mediaType: "text/plain", wantCandidate: false},
		{mediaType: "text/html; charset=utf-8", wantCandidate: false},
		{mediaType: "multipart/alternative", wantCandidate: false},
		{filename: "note.txt", mediaType: "text/plain", wantCandidate: true},
		{mediaType: "text/plain", disposition: true, wantCandidate: true},
		{mediaType: "image/png", wantCandidate: true},
		{mediaType: "text/plain", attachmentID: "attachment-1", wantCandidate: true},
	} {
		got := gmailAttachmentCandidate(test.filename, test.mediaType, test.attachmentID, test.disposition)
		if got != test.wantCandidate {
			t.Fatalf("candidate filename=%q media_type=%q attachment_id=%t disposition=%t got=%t want=%t", test.filename, test.mediaType, test.attachmentID != "", test.disposition, got, test.wantCandidate)
		}
	}
}

func TestProviderClassifiesFailuresWithoutResponseDetails(t *testing.T) {
	for _, test := range []struct {
		status int
		kind   ErrorKind
	}{{401, ErrorAuthorization}, {403, ErrorAuthorization}, {429, ErrorQuota}, {503, ErrorTransient}, {404, ErrorPermanent}} {
		err := classify(test.status, "5")
		var providerError *ProviderError
		if !errors.As(err, &providerError) || providerError.Kind != test.kind || providerError.StatusCode != test.status || providerError.RetryAfter != 5*time.Second {
			t.Fatalf("status %d classified as %+v", test.status, err)
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatal("provider error exposed response details")
		}
		if test.status == http.StatusNotFound && providerError.SyncFailureReason() != string(ReasonNotFound) {
			t.Fatalf("not found reason = %q", providerError.SyncFailureReason())
		}
	}
	quota := classify(http.StatusForbidden, "", []byte(`{"error":{"errors":[{"reason":"userRateLimitExceeded"}],"message":"private provider detail"}}`))
	var quotaError *ProviderError
	if !errors.As(quota, &quotaError) || quotaError.Kind != ErrorQuota || strings.Contains(quota.Error(), "private") {
		t.Fatalf("quota error = %v", quota)
	}
}

func TestProviderFailureReasonIsClosedAndContentFree(t *testing.T) {
	for _, test := range []struct {
		reason FailureReason
		want   string
	}{
		{reason: ReasonNotFound, want: "not_found"},
		{reason: ReasonRejected, want: "rejected"},
		{reason: ReasonInvalidPayload, want: "invalid_payload"},
		{reason: ReasonAttachmentMapping, want: "attachment_mapping"},
		{reason: ReasonInvalidEnvelope, want: "invalid_envelope"},
		{reason: FailureReason("provider-private-detail"), want: ""},
	} {
		err := &ProviderError{Kind: ErrorPermanent, Reason: test.reason, StatusCode: 418}
		if got := err.SyncFailureReason(); got != test.want || strings.Contains(got, "private") || strings.Contains(err.Error(), "private") {
			t.Fatalf("reason %q exposed as %q with error %q", test.reason, got, err.Error())
		}
	}
}

func TestProviderPreservesPageProgressForBoundedMIMEFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "malformed", err: mail.ErrMalformedMIME},
		{name: "oversized", err: mail.ErrMIMETooLarge},
		{name: "too many parts", err: mail.ErrTooManyParts},
		{name: "wrapped malformed", err: errors.Join(errors.New("normalization failed"), mail.ErrMalformedMIME)},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider, server, _ := gmailFixture(t)
			defer server.Close()
			provider.normalizer = failingNormalizer{err: test.err}
			before := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)

			page, err := provider.Backfill(context.Background(), mail.SyncCursor{}, nil, &before, 25)
			if err != nil || len(page.Messages) != 1 || !page.HasMore || string(page.NextCursor.Value) != "backfill-page-2" {
				t.Fatalf("degraded backfill messages=%d has_more=%t cursor=%q error=%v", len(page.Messages), page.HasMore, page.NextCursor.Value, err)
			}
			message := page.Messages[0]
			if message.RemoteID != "message-1" || message.ThreadID != "thread-1" || message.SentAt.IsZero() || len(message.LabelIDs) == 0 {
				t.Fatalf("provider envelope was not preserved: %+v", message)
			}
			if message.Content.Subject != "" || message.Content.MessageID != "" || len(message.Content.References) != 0 || len(message.Content.InReplyTo) != 0 || len(message.Content.Addresses) != 0 || message.Content.BodyText != "" || message.Content.BodyHTML != "" || len(message.Content.Attachments) != 0 {
				t.Fatalf("unsafe partial content was retained: %+v", message.Content)
			}
		})
	}
}

func TestProviderFailsClosedForUnknownNormalizerFailure(t *testing.T) {
	provider, server, _ := gmailFixture(t)
	defer server.Close()
	want := errors.New("normalizer unavailable")
	provider.normalizer = failingNormalizer{err: want}
	before := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)

	page, err := provider.Backfill(context.Background(), mail.SyncCursor{}, nil, &before, 25)
	if !errors.Is(err, want) || len(page.Messages) != 0 {
		t.Fatalf("unknown failure page=%+v error=%v", page, err)
	}
}

func TestHistoryCursorRejectsWrongProvider(t *testing.T) {
	provider, server, _ := gmailFixture(t)
	defer server.Close()
	_, err := provider.Changes(context.Background(), mail.SyncCursor{Kind: "microsoft_delta", Value: []byte(`{"historyId":"100"}`)})
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cursor error = %v", err)
	}
}

func TestDecodeBase64URLAcceptsPaddedAndUnpaddedValues(t *testing.T) {
	payload := []byte("sanitized fixture")
	for _, encoded := range []string{
		base64.RawURLEncoding.EncodeToString(payload),
		base64.URLEncoding.EncodeToString(payload),
	} {
		decoded, ok := decodeBase64URL(encoded, len(payload))
		if !ok || string(decoded) != string(payload) {
			t.Fatalf("valid base64url rejected: ok=%t decoded_bytes=%d", ok, len(decoded))
		}
	}
	for _, invalid := range []struct {
		value string
		limit int
	}{
		{value: "%%%", limit: 32},
		{value: base64.URLEncoding.EncodeToString(payload), limit: len(payload) - 1},
	} {
		if decoded, ok := decodeBase64URL(invalid.value, invalid.limit); ok || decoded != nil {
			t.Fatalf("invalid base64url accepted: limit=%d decoded_bytes=%d", invalid.limit, len(decoded))
		}
	}
}

func TestProviderDiscardsOversizedRawPayloadAndRejectsMalformedEncoding(t *testing.T) {
	provider, server, _ := gmailFixture(t)
	defer server.Close()

	const limit = 32
	oversized := base64.RawURLEncoding.EncodeToString(make([]byte, limit+1))
	content, err := provider.normalizeRawPayload(oversized, limit)
	if err != nil || !reflect.DeepEqual(content, mail.NormalizedMessageContent{}) {
		t.Fatalf("oversized payload content=%+v error=%v", content, err)
	}

	content, err = provider.normalizeRawPayload("%%%", limit)
	var providerError *ProviderError
	if !errors.As(err, &providerError) || providerError.SyncFailureReason() != string(ReasonInvalidPayload) || !reflect.DeepEqual(content, mail.NormalizedMessageContent{}) {
		t.Fatalf("malformed payload content=%+v error=%v", content, err)
	}
}

func writeJSON(response http.ResponseWriter, value any) {
	_ = json.NewEncoder(response).Encode(value)
}

func TestBackfillCursorEncodesPageToken(t *testing.T) {
	value, err := decodePageCursor(mail.SyncCursor{Kind: "google_backfill", Value: []byte("page-token")}, "google_backfill")
	if err != nil || value != "page-token" {
		t.Fatalf("page token = %q, %v", value, err)
	}
	encoded := url.QueryEscape(value)
	if encoded != "page-token" {
		t.Fatalf("escaped token = %q", encoded)
	}
}
