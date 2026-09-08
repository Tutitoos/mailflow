package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

func TestRedisReplayAndCursorExpiry(t *testing.T) {
	client, prefix := testkit.Redis(t)
	config := DefaultConfig()
	config.Prefix = prefix
	config.MaxEvents = 2
	config.ReplayLimit = 2
	config.ReadBlock = 20 * time.Millisecond
	store, err := NewStore(context.Background(), client, config)
	if err != nil {
		t.Fatal(err)
	}
	userID := "0199ed3b-c950-7000-8000-000000000017"
	first, err := store.Publish(context.Background(), userID, "system.status", json.RawMessage(`{"status":"starting"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Publish(context.Background(), userID, "sync.progress", json.RawMessage(`{"state":"running"}`))
	if err != nil {
		t.Fatal(err)
	}
	third, err := store.Publish(context.Background(), userID, "system.status", json.RawMessage(`{"status":"ready"}`))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.Replay(context.Background(), userID, first.Cursor); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("trimmed cursor error = %v", err)
	}
	replayed, latest, err := store.Replay(context.Background(), userID, second.Cursor)
	if err != nil || len(replayed) != 1 || replayed[0].Cursor != third.Cursor || latest != third.Cursor {
		t.Fatalf("replay = %+v, latest = %q, error = %v", replayed, latest, err)
	}
	repeated, _, err := store.Replay(context.Background(), userID, third.Cursor)
	if err != nil || len(repeated) != 0 {
		t.Fatalf("acknowledged event replayed: %+v, error = %v", repeated, err)
	}
}

func TestLiveEventsAreScopedAndPayloadsAreSanitized(t *testing.T) {
	client, prefix := testkit.Redis(t)
	config := DefaultConfig()
	config.Prefix = prefix
	config.ReadBlock = 50 * time.Millisecond
	store, err := NewStore(context.Background(), client, config)
	if err != nil {
		t.Fatal(err)
	}
	owner := "0199ed3b-c950-7000-8000-000000000017"
	other := "0199ed3b-c950-7000-8000-000000000099"
	_, cursor, err := store.Replay(context.Background(), owner, "")
	if err != nil {
		t.Fatal(err)
	}
	published, err := store.Publish(context.Background(), owner, "admin.log", json.RawMessage(`{"event":"queue.handled","level":"info"}`))
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.Next(context.Background(), owner, cursor)
	if err != nil || len(events) != 1 || events[0].Cursor != published.Cursor {
		t.Fatalf("live events = %+v, error = %v", events, err)
	}
	foreign, _, err := store.Replay(context.Background(), other, cursor)
	if !errors.Is(err, ErrCursorExpired) || len(foreign) != 0 {
		t.Fatalf("foreign cursor result = %+v, error = %v", foreign, err)
	}
	for _, payload := range []json.RawMessage{
		json.RawMessage(`{"accessToken":"secret"}`),
		json.RawMessage(`{"message":{"subject":"private"}}`),
		json.RawMessage(`{"value":"owner@example.test"}`),
		json.RawMessage(`[]`),
		json.RawMessage(`not-json`),
	} {
		if _, err := store.Publish(context.Background(), owner, "system.status", payload); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("unsafe payload error = %v", err)
		}
	}
}

func TestTranslationChangeIsAValidBoundedEvent(t *testing.T) {
	if !validType("translations.changed") {
		t.Fatal("translation invalidation event type is not registered")
	}
	if !safePayload(json.RawMessage(`{"revision":42}`), DefaultConfig().MaxPayloadBytes) {
		t.Fatal("bounded translation invalidation payload was rejected")
	}
}

func TestLiveReadIsBoundedForSlowConsumers(t *testing.T) {
	client, prefix := testkit.Redis(t)
	config := DefaultConfig()
	config.Prefix = prefix
	config.ReadBlock = 20 * time.Millisecond
	store, err := NewStore(context.Background(), client, config)
	if err != nil {
		t.Fatal(err)
	}
	userID := "0199ed3b-c950-7000-8000-000000000017"
	_, cursor, err := store.Replay(context.Background(), userID, "")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 100; index++ {
		if _, err := store.Publish(context.Background(), userID, "system.status", json.RawMessage(`{"status":"busy"}`)); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := store.Next(context.Background(), userID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) == 0 || len(batch) > 32 {
		t.Fatalf("live batch size = %d, want 1..32", len(batch))
	}
}
