package httpapi_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
)

type fakeMailReader struct {
	accountID string
	category  mail.Category
	cursor    *mail.ThreadCursor
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
	if userID != testUserID || accountID != reader.accountID {
		return mail.Thread{}, mail.ErrThreadNotFound
	}
	return mail.Thread{ID: threadID, AccountID: accountID, Category: mail.CategoryPrimary, LastMessageAt: time.Date(2026, 9, 7, 17, 0, 0, 0, time.UTC)}, nil
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

func TestInboxEndpointsRequireScopeAndExposeOpaqueCursor(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := &rotatingJWKS{kid: "mail", key: publicKey}
	server := httptest.NewServer(keys)
	defer server.Close()
	reader := &fakeMailReader{}
	registry := metrics.NewRegistry()
	app := httpapi.New(httpapi.Dependencies{
		Admin: admin.NewService("test", registry), AuthAudience: testAudience, AuthIssuer: testIssuer,
		AuthJWKSURL: server.URL, CurrentUsers: fakeUserResolver{user: authbridge.User{ID: testUserID, Email: "owner@example.test", Locale: "en"}},
		Inbox: reader, Mailboxes: reader, Threads: reader, Sentry: mailflowsentry.NewService(1024), Translations: translations.NewCatalog(),
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
}
