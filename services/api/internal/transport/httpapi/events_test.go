package httpapi_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/admin"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/translations"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/Tutitoos/mailflow/services/api/internal/transport/httpapi"
	"github.com/coder/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

type recordingClientActivity struct{ activated chan string }

func (activity recordingClientActivity) Activate(_ context.Context, userID, accountID string) error {
	activity.activated <- userID + ":" + accountID
	return nil
}

func TestWebSocketAuthenticationReplayExpiryAndShutdown(t *testing.T) {
	client, prefix := testkit.Redis(t)
	config := events.DefaultConfig()
	config.Prefix = prefix
	config.MaxEvents = 2
	config.ReadBlock = 20 * time.Millisecond
	store, err := events.NewStore(context.Background(), client, config)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := &rotatingJWKS{kid: "events", key: publicKey}
	keyServer := httptest.NewServer(keys)
	defer keyServer.Close()
	shutdown, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := metrics.NewRegistry()
	accountID := "0199ed3b-c950-7000-8000-000000000016"
	activity := recordingClientActivity{activated: make(chan string, 4)}
	app := httpapi.New(httpapi.Dependencies{
		Admin: admin.NewService("test", registry), AuthAudience: testAudience, AuthIssuer: testIssuer,
		AuthJWKSURL: keyServer.URL, CurrentUsers: fakeUserResolver{user: authbridge.User{ID: testUserID, Email: "owner@example.test", Locale: "en"}},
		Accounts:       fakeAccountLister{items: []accounts.Account{{ID: accountID, Provider: accounts.ProviderGoogle}}},
		ClientActivity: activity, Events: store, Sentry: mailflowsentry.NewService(1024), Translations: translations.NewCatalog(), Shutdown: shutdown,
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- app.Listener(listener, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() {
		_ = app.ShutdownWithTimeout(time.Second)
		<-serveDone
	})
	url := "ws://" + listener.Addr().String() + "/api/v1/events"

	unauthorized, response, err := websocket.Dial(context.Background(), url, nil)
	if unauthorized != nil {
		_ = unauthorized.Close(websocket.StatusNormalClosure, "")
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upgrade: response=%v error=%v", response, err)
	}

	token := signToken(t, privateKey, "events", jwt.RegisteredClaims{
		Audience: jwt.ClaimStrings{testAudience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)), Issuer: testIssuer, Subject: testUserID,
	})
	browser := dialBrowserEvents(t, url, token)
	if browser.Subprotocol() != "mailflow.v1" {
		t.Fatalf("negotiated subprotocol = %q", browser.Subprotocol())
	}
	select {
	case activated := <-activity.activated:
		if activated != testUserID+":"+accountID {
			t.Fatalf("activated account = %q", activated)
		}
	case <-time.After(time.Second):
		t.Fatal("connected browser did not activate account polling")
	}
	_ = browser.Close(websocket.StatusNormalClosure, "browser authenticated")
	first, err := store.Publish(context.Background(), testUserID, "system.status", json.RawMessage(`{"status":"one"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Publish(context.Background(), testUserID, "system.status", json.RawMessage(`{"status":"two"}`))
	if err != nil {
		t.Fatal(err)
	}
	connection := dialEvents(t, url+"?cursor="+first.Cursor, token)
	replayed := readEvent(t, connection)
	if replayed.Cursor != second.Cursor {
		t.Fatalf("replayed cursor = %q, want %q", replayed.Cursor, second.Cursor)
	}
	third, err := store.Publish(context.Background(), testUserID, "admin.alert", json.RawMessage(`{"code":"worker_stalled"}`))
	if err != nil {
		t.Fatal(err)
	}
	live := readEvent(t, connection)
	if live.Cursor != third.Cursor || live.Version != events.EnvelopeVersion {
		t.Fatalf("live event = %+v", live)
	}
	_ = connection.Close(websocket.StatusNormalClosure, "done")

	expired := dialEvents(t, url+"?cursor="+first.Cursor, token)
	resync := readEvent(t, expired)
	if resync.Type != "system.resync_required" || resync.Cursor != third.Cursor {
		t.Fatalf("resync event = %+v", resync)
	}
	cancel()
	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	_, _, err = expired.Read(readCtx)
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("shutdown close status = %v, error = %v", websocket.CloseStatus(err), err)
	}
}

func dialBrowserEvents(t *testing.T, url, token string) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Origin", testIssuer)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: header, Subprotocols: []string{"mailflow.v1", "mailflow.bearer." + token},
	})
	if err != nil {
		t.Fatalf("dial browser events: status=%v error=%v", response, err)
	}
	return connection
}

func dialEvents(t *testing.T, url, token string) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	header.Set("Origin", testIssuer)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial events: status=%v error=%v", response, err)
	}
	return connection
}

func readEvent(t *testing.T, connection *websocket.Conn) events.Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var event events.Envelope
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	return event
}
