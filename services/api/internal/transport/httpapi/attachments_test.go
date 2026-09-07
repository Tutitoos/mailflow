package httpapi_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/golang-jwt/jwt/v5"
)

type fakeAttachmentReader struct {
	path     string
	objectID string
}

func (reader fakeAttachmentReader) OpenAttachment(_ context.Context, user, object string, _ time.Time) (cdn.Attachment, *os.File, error) {
	if user != testUserID || object != reader.objectID {
		return cdn.Attachment{}, nil, cdn.ErrAttachmentNotFound
	}
	file, err := os.Open(reader.path)
	filename := "mailflow.txt"
	return cdn.Attachment{ObjectID: object, Filename: &filename, MediaType: "text/plain", SizeBytes: 8, ETag: strings.Repeat("a", 64)}, file, err
}

func TestAuthenticatedAttachmentDownloadSupportsRangesAndConditions(t *testing.T) {
	path := t.TempDir() + "/attachment"
	if err := os.WriteFile(path, []byte("mailflow"), 0o600); err != nil {
		t.Fatal(err)
	}
	objectID := "0123456789abcdef0123456789abcdef"
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := &rotatingJWKS{kid: "current", key: publicKey}
	server := httptest.NewServer(keys)
	defer server.Close()
	user := authbridge.User{ID: testUserID, Email: "owner@example.test", Locale: "en"}
	app := authenticatedAppWithDependencies(server.URL, fakeUserResolver{user: user}, nil, fakeAttachmentReader{path: path, objectID: objectID})
	token := signToken(t, privateKey, "current", jwt.RegisteredClaims{
		Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), Issuer: testIssuer, Subject: testUserID,
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+objectID, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Range", "bytes=2-5")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusPartialContent || string(body) != "ilfl" || response.Header.Get("Content-Range") != "bytes 2-5/8" || response.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("range response status=%d body=%q headers=%v", response.StatusCode, body, response.Header)
	}
	etag := `"` + strings.Repeat("a", 64) + `"`
	if response.Header.Get("ETag") != etag || !strings.Contains(response.Header.Get("Content-Disposition"), "mailflow.txt") {
		t.Fatalf("download headers = %v", response.Header)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+objectID, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("If-None-Match", etag)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional response status=%d error=%v", response.StatusCode, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+objectID, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Range", "bytes=20-30")
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusRequestedRangeNotSatisfiable || response.Header.Get("Content-Range") != "bytes */8" {
		t.Fatalf("invalid range response status=%d headers=%v error=%v", response.StatusCode, response.Header, err)
	}
}

func TestAttachmentDownloadRequiresAuthenticationAndHidesOwnership(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := &rotatingJWKS{kid: "current", key: publicKey}
	server := httptest.NewServer(keys)
	defer server.Close()
	user := authbridge.User{ID: testUserID, Email: "owner@example.test", Locale: "en"}
	app := authenticatedAppWithDependencies(server.URL, fakeUserResolver{user: user}, nil, fakeAttachmentReader{objectID: "0123456789abcdef0123456789abcdef"})
	path := "/api/v1/attachments/ffffffffffffffffffffffffffffffff"
	response, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d error=%v", response.StatusCode, err)
	}
	token := signToken(t, privateKey, "current", jwt.RegisteredClaims{Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), Issuer: testIssuer, Subject: testUserID})
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = app.Test(request)
	if err != nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown attachment status=%d error=%v", response.StatusCode, err)
	}
}
