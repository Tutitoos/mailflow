package httpapi_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
)

type fakeMailReader struct {
	accountID    string
	category     mail.Category
	cursor       *mail.ThreadCursor
	search       mail.SearchQuery
	searchCursor *mail.SearchCursor
}

func (reader *fakeMailReader) ListInbox(_ context.Context, userID, accountID string, category mail.Category, cursor *mail.ThreadCursor, limit int) (mail.InboxPage, error) {
	if userID != testUserID || limit != 1 {
		return mail.InboxPage{}, mail.ErrInvalidThread
	}
	reader.accountID, reader.category, reader.cursor = accountID, category, cursor
	next := &mail.ThreadCursor{ID: "0199ed3b-c950-7000-8000-000000000019", LastMessageAt: time.Date(2026, 9, 7, 17, 0, 0, 0, time.UTC)}
	return mail.InboxPage{Items: []mail.InboxThread{{ID: next.ID, AccountID: accountID, SenderName: "Fixture sender", SenderAddress: "sender@example.test", Subject: "", Preview: "", LastMessageAt: next.LastMessageAt, Category: category}}, Next: next}, nil
}

func (reader *fakeMailReader) ListMailboxes(_ context.Context, _, accountID string) ([]mail.Mailbox, error) {
	return []mail.Mailbox{{ID: "0199ed3b-c950-7000-8000-000000000020", AccountID: accountID, RemoteID: "INBOX", RemoteName: "Inbox", Role: mail.MailboxInbox}}, nil
}

func (reader *fakeMailReader) ListLabels(_ context.Context, _, accountID string) ([]mail.Label, error) {
	category := mail.CategoryPrimary
	return []mail.Label{{ID: "0199ed3b-c950-7000-8000-000000000021", AccountID: accountID, RemoteName: "Primary", Kind: mail.LabelCategory, Category: &category}}, nil
}

func (reader *fakeMailReader) GetThread(_ context.Context, userID, accountID, threadID string) (mail.Thread, error) {
	if userID != testUserID || accountID != reader.accountID || strings.HasSuffix(threadID, "0099") {
		return mail.Thread{}, mail.ErrThreadNotFound
	}
	return mail.Thread{ID: threadID, AccountID: accountID, Category: mail.CategoryPrimary, LastMessageAt: time.Date(2026, 9, 7, 17, 0, 0, 0, time.UTC)}, nil
}

type fakeActionStore struct{ inputs []mail.EnqueueActionInput }

func (store *fakeActionStore) Enqueue(ctx context.Context, input mail.EnqueueActionInput, apply mail.ApplyActionState) (mail.PendingAction, bool, error) {
	store.inputs = append(store.inputs, input)
	if err := apply(ctx, nil); err != nil {
		return mail.PendingAction{}, false, err
	}
	return mail.PendingAction{ID: "0199ed3b-c950-7000-8000-000000000088", AccountID: input.AccountID, Kind: input.Kind, TargetID: input.TargetID, Status: mail.ActionPending}, true, nil
}
func (*fakeActionStore) Claim(context.Context, string) (mail.ActionClaim, error) {
	return mail.ActionClaim{}, mail.ErrActionUnavailable
}
func (*fakeActionStore) Complete(context.Context, string, mail.ActionClaim) (mail.PendingAction, error) {
	return mail.PendingAction{}, nil
}
func (*fakeActionStore) Fail(context.Context, string, mail.ActionClaim, string, time.Time, json.RawMessage, mail.ApplyActionState) (mail.PendingAction, bool, error) {
	return mail.PendingAction{}, false, nil
}

type fakeActionState struct{}

func (fakeActionState) ApplyActionState(context.Context, pgx.Tx, mail.EnqueueActionInput, json.RawMessage) error {
	return nil
}

func (fakeActionState) ThreadHasLabel(context.Context, string, string, string, string) (bool, error) {
	return false, nil
}

func (reader *fakeMailReader) ListMessages(_ context.Context, userID, accountID, threadID string, cursor *mail.MessageCursor, limit int) (mail.MessagePage, error) {
	if userID != testUserID || accountID != reader.accountID || limit != 100 {
		return mail.MessagePage{}, mail.ErrInvalidMessage
	}
	return mail.MessagePage{Items: []mail.Message{{
		ID: "0199ed3b-c950-7000-8000-000000000022", ThreadID: threadID,
		AccountID: accountID, SentAt: time.Date(2026, 9, 7, 17, 0, 0, 0, time.UTC),
		Addresses: []mail.MessageAddress{}, Attachments: []mail.Attachment{},
	}}}, nil
}

func (reader *fakeMailReader) SearchMessages(_ context.Context, userID, accountID string, query mail.SearchQuery, cursor *mail.SearchCursor, limit int) (mail.SearchPage, error) {
	if userID != testUserID || accountID != reader.accountID || limit != 1 {
		return mail.SearchPage{}, &mail.SearchValidationError{Code: "invalid_query"}
	}
	reader.search, reader.searchCursor = query, cursor
	displayName := "Fixture sender"
	next := &mail.SearchCursor{Rank: 0.75, SentAt: time.Date(2026, 9, 7, 17, 0, 0, 0, time.UTC), ID: "0199ed3b-c950-7000-8000-000000000023"}
	return mail.SearchPage{Items: []mail.SearchHit{{Rank: next.Rank, Message: mail.Message{
		ID: next.ID, ThreadID: "0199ed3b-c950-7000-8000-000000000019", AccountID: accountID,
		RemoteID: "provider-secret", Subject: "Fixture subject", BodyText: "Safe result preview", BodyHTML: "<p>private</p>",
		SentAt: next.SentAt, Addresses: []mail.MessageAddress{{Role: mail.AddressFrom, DisplayName: &displayName, Address: "sender@example.test"}},
		Attachments: []mail.Attachment{{ID: "0199ed3b-c950-7000-8000-000000000024"}},
	}}}, Next: next}, nil
}

func TestInboxEndpointsRequireScopeAndExposeOpaqueCursor(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := &rotatingJWKS{kid: "mail", key: publicKey}
	server := httptest.NewServer(keys)
	defer server.Close()
	reader := &fakeMailReader{}
	actionStore := &fakeActionStore{}
	registry := metrics.NewRegistry()
	app := httpapi.New(httpapi.Dependencies{
		Admin: admin.NewService("test", registry), AuthAudience: testAudience, AuthIssuer: testIssuer,
		AuthJWKSURL: server.URL, CurrentUsers: fakeUserResolver{user: authbridge.User{ID: testUserID, Email: "owner@example.test", Locale: "en"}},
		Inbox: reader, Mailboxes: reader, Search: reader, Threads: reader,
		Actions: mail.NewPendingActionService(actionStore, nil), ActionState: fakeActionState{},
		Sentry: mailflowsentry.NewService(1024), Translations: translations.NewCatalog(),
	})
	token := signToken(t, privateKey, "mail", jwt.RegisteredClaims{Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), Issuer: testIssuer, Subject: testUserID})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/threads?accountId=0199ed3b-c950-7000-8000-000000000018&category=primary&limit=1", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := app.Test(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("list inbox: status=%d error=%v", response.StatusCode, err)
	}
	var page struct {
		Items      []mail.InboxThread `json:"items"`
		NextCursor *string            `json:"nextCursor"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.NextCursor == nil || *page.NextCursor == "" || reader.category != mail.CategoryPrimary {
		t.Fatalf("unexpected inbox page: %+v", page)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/threads?accountId="+reader.accountID+"&category=primary&limit=1&cursor="+*page.NextCursor, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusOK || reader.cursor == nil {
		t.Fatalf("resume inbox: status=%d cursor=%+v error=%v", response.StatusCode, reader.cursor, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/threads?category=primary", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing account scope: status=%d error=%v", response.StatusCode, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/threads/0199ed3b-c950-7000-8000-000000000019?accountId="+reader.accountID, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("get conversation: status=%d error=%v", response.StatusCode, err)
	}
	var conversation struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&conversation); err != nil || len(conversation.Messages) != 1 {
		t.Fatalf("decode conversation: messages=%d error=%v", len(conversation.Messages), err)
	}
	for _, forbidden := range []string{"remoteId", "messageId", "references", "contentId"} {
		if strings.Contains(string(conversation.Messages[0]), forbidden) {
			t.Fatalf("conversation exposed provider-only field %q: %s", forbidden, conversation.Messages[0])
		}
	}

	searchPath := "/api/v1/search?accountId=" + reader.accountID + "&limit=1&q=" + url.QueryEscape("quarterly from:sender@example.test")
	request = httptest.NewRequest(http.MethodGet, searchPath, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("search mail: status=%d error=%v", response.StatusCode, err)
	}
	var results struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor *string           `json:"nextCursor"`
	}
	if err := json.NewDecoder(response.Body).Decode(&results); err != nil || len(results.Items) != 1 || results.NextCursor == nil {
		t.Fatalf("decode search: items=%d cursor=%v error=%v", len(results.Items), results.NextCursor, err)
	}
	if reader.search.Text != "quarterly" || len(reader.search.From) != 1 {
		t.Fatalf("search query not parsed: %+v", reader.search)
	}
	for _, forbidden := range []string{"remoteId", "bodyHtml", "provider-secret"} {
		if strings.Contains(string(results.Items[0]), forbidden) {
			t.Fatalf("search exposed provider-only field %q: %s", forbidden, results.Items[0])
		}
	}

	request = httptest.NewRequest(http.MethodGet, searchPath+"&cursor="+url.QueryEscape(*results.NextCursor), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusOK || reader.searchCursor == nil {
		t.Fatalf("resume search: status=%d cursor=%+v error=%v", response.StatusCode, reader.searchCursor, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/search?accountId="+reader.accountID+"&q=after%3Ayesterday", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid search: status=%d error=%v", response.StatusCode, err)
	}
	var problemBody map[string]any
	if err := json.NewDecoder(response.Body).Decode(&problemBody); err != nil || problemBody["code"] != "search_invalid_value" {
		t.Fatalf("invalid search problem = %+v, error=%v", problemBody, err)
	}

	actionBody := `{"accountId":"` + reader.accountID + `","kind":"archive","targetIds":["0199ed3b-c950-7000-8000-000000000019","0199ed3b-c950-7000-8000-000000000099"]}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/actions", strings.NewReader(actionBody))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "ui-action-0000000001")
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusAccepted {
		t.Fatalf("create actions: status=%d error=%v", response.StatusCode, err)
	}
	var actionResponse struct {
		Items []struct {
			TargetID string `json:"targetId"`
			ActionID string `json:"actionId"`
			Error    string `json:"error"`
		} `json:"items"`
		Partial bool `json:"partial"`
	}
	if err := json.NewDecoder(response.Body).Decode(&actionResponse); err != nil || len(actionResponse.Items) != 2 || !actionResponse.Partial || len(actionStore.inputs) != 1 {
		t.Fatalf("action response = %+v inputs=%d error=%v", actionResponse, len(actionStore.inputs), err)
	}
	if actionStore.inputs[0].IdempotencyKey == "ui-action-0000000001" || string(actionStore.inputs[0].DesiredState) != `{"archived":true}` {
		t.Fatalf("action input = %+v", actionStore.inputs[0])
	}
}
