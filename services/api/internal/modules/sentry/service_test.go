package sentry_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIngestionAcceptsSDKEnvelopesAndPersistsOnlySafeMetadata(t *testing.T) {
	ctx := context.Background()
	service, pool, store, projects := persistentService(t, sentry.DefaultConfig())
	now := time.Now().UTC()
	sdks := []string{"sentry.javascript.browser", "sentry.go", "sentry.rust", "sentry.cocoa"}
	for index, project := range projects {
		eventID := fmt.Sprintf("%032x", index+1)
		payload := []byte(fmt.Sprintf(`{"event_id":%q,"environment":"production","release":"abcdef1","level":"error","sdk":{"name":%q},"exception":{"values":[{"type":"Error","value":"owner@example.test"}]},"breadcrumbs":{"values":[{"message":"private subject"}]}}`, eventID, sdks[index]))
		receipt, err := service.Ingest(ctx, sentry.Request{Authorization: "Sentry sentry_key=" + project.PublicKey + ", sentry_version=7", Body: envelope(eventID, "event", payload), Now: now})
		if err != nil || receipt.ID != eventID {
			t.Fatalf("%s receipt=%+v err=%v", project.Component, receipt, err)
		}
	}
	duplicatePayload := []byte(`{"event_id":"00000000000000000000000000000001","sdk":{"name":"sentry.javascript.browser"}}`)
	duplicateReceipt, err := service.Ingest(ctx, sentry.Request{QueryKey: projects[0].PublicKey, Body: envelope("00000000000000000000000000000001", "event", duplicatePayload), Now: now})
	if err != nil || duplicateReceipt.ID != "00000000000000000000000000000001" {
		t.Fatalf("duplicate receipt=%+v err=%v", duplicateReceipt, err)
	}

	var events, items int
	if err := pool.QueryRow(ctx, `select count(*) from sentry_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from sentry_event_items`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if events != 4 || items != 4 {
		t.Fatalf("events=%d items=%d", events, items)
	}
	var summaries string
	if err := pool.QueryRow(ctx, `select string_agg(summary::text, ' ') from sentry_event_items`).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(summaries, "owner@example.test") || strings.Contains(summaries, "private subject") {
		t.Fatalf("private payload persisted: %s", summaries)
	}

	largeID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	largePayload := []byte(`{"event_id":"` + largeID + `","sdk":{"name":"sentry.go"},"extra":{"padding":"` + strings.Repeat("x", 70<<10) + `"}}`)
	if _, err := service.Ingest(ctx, sentry.Request{QueryKey: projects[0].PublicKey, Body: envelope(largeID, "event", largePayload), Now: now}); err != nil {
		t.Fatal(err)
	}
	var objectID string
	if err := pool.QueryRow(ctx, `select payload_object_id from sentry_event_items where payload_object_id is not null`).Scan(&objectID); err != nil {
		t.Fatal(err)
	}
	file, err := store.Open("sentry", objectID)
	if err != nil {
		t.Fatal(err)
	}
	storedSummary, _ := io.ReadAll(file)
	_ = file.Close()
	if strings.Contains(string(storedSummary), strings.Repeat("x", 100)) || !json.Valid(storedSummary) {
		t.Fatal("large CDN object was not a safe normalized summary")
	}
}

func TestIngestionRejectsUnsafeRequestsAndBoundsResources(t *testing.T) {
	ctx := context.Background()
	config := sentry.DefaultConfig()
	config.RatePerMinute = 1
	service, _, _, projects := persistentService(t, config)
	now := time.Now().UTC()
	payload := []byte(`{"event_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","sdk":{"name":"sentry.go"}}`)
	body := envelope("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "event", payload)
	if _, err := service.Ingest(ctx, sentry.Request{Body: body, Now: now}); !errors.Is(err, sentry.ErrUnauthenticated) {
		t.Fatalf("unauthenticated err=%v", err)
	}
	if _, err := service.Ingest(ctx, sentry.Request{QueryKey: projects[0].PublicKey, Body: []byte("not-an-envelope"), Now: now}); !errors.Is(err, sentry.ErrInvalidEnvelope) {
		t.Fatalf("invalid err=%v", err)
	}
	if _, err := service.Ingest(ctx, sentry.Request{QueryKey: projects[0].PublicKey, Body: body, Now: now}); !errors.Is(err, sentry.ErrRateLimited) {
		t.Fatalf("rate err=%v", err)
	}

	overConfig := sentry.DefaultConfig()
	overConfig.MaxEnvelopeBytes = 4
	over, _, _, overProjects := persistentService(t, overConfig)
	if _, err := over.Ingest(ctx, sentry.Request{QueryKey: overProjects[0].PublicKey, Body: []byte("large")}); !errors.Is(err, sentry.ErrEnvelopeTooLarge) {
		t.Fatalf("size err=%v", err)
	}

	quotaConfig := sentry.DefaultConfig()
	quotaConfig.StorageQuota = 8
	quota, _, _, quotaProjects := persistentService(t, quotaConfig)
	if _, err := quota.Ingest(ctx, sentry.Request{QueryKey: quotaProjects[0].PublicKey, Body: body}); !errors.Is(err, sentry.ErrStorageQuota) {
		t.Fatalf("quota err=%v", err)
	}
}

func TestLegacyAttachmentAndRetentionContracts(t *testing.T) {
	ctx := context.Background()
	service, pool, _, projects := persistentService(t, sentry.DefaultConfig())
	now := time.Now().UTC()
	legacyID := "cccccccccccccccccccccccccccccccc"
	legacy := []byte(`{"event_id":"` + legacyID + `","sdk":{"name":"sentry.cocoa"}}`)
	if _, err := service.Ingest(ctx, sentry.Request{QueryKey: projects[3].PublicKey, LegacyEventID: legacyID, Legacy: true, Body: legacy, Now: now}); err != nil {
		t.Fatal(err)
	}
	attachmentID := "dddddddddddddddddddddddddddddddd"
	attachment := []byte("private attachment owner@example.test")
	if _, err := service.Ingest(ctx, sentry.Request{QueryKey: projects[0].PublicKey, Body: envelope(attachmentID, "attachment", attachment), Now: now.Add(-sentry.DefaultRetention - time.Hour)}); err != nil {
		t.Fatal(err)
	}
	var discarded bool
	var objectCount int
	if err := pool.QueryRow(ctx, `select discarded from sentry_event_items where item_type='attachment'`).Scan(&discarded); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from cdn_objects where namespace='sentry'`).Scan(&objectCount); err != nil {
		t.Fatal(err)
	}
	if !discarded || objectCount != 0 {
		t.Fatalf("attachment discarded=%v objects=%d", discarded, objectCount)
	}
	deleted, err := service.Cleanup(ctx, now)
	if err != nil || deleted != 1 {
		t.Fatalf("cleanup deleted=%d err=%v", deleted, err)
	}
}

func persistentService(t *testing.T, config sentry.Config) (*sentry.Service, *pgxpool.Pool, *cdn.Store, []sentry.Project) {
	t.Helper()
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
	store, err := cdn.NewStore(t.TempDir(), int64(config.MaxEnvelopeBytes))
	if err != nil {
		t.Fatal(err)
	}
	service, err := sentry.NewPersistentService(pool, store, config)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := sentry.DerivedProjects([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureProjects(ctx, projects, time.Now().UTC()); err != nil {
		t.Fatalf("configure projects: %v", err)
	}
	return service, pool, store, projects
}

func envelope(eventID, itemType string, payload []byte) []byte {
	return []byte(fmt.Sprintf("{\"event_id\":%q}\n{\"type\":%q,\"length\":%d,\"content_type\":\"application/json\"}\n%s", eventID, itemType, len(payload), payload))
}
