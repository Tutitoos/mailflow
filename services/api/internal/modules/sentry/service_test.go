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
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

type recordingQueue struct {
	job queue.Job
}

func (recorder *recordingQueue) Enqueue(_ context.Context, kind string, payload json.RawMessage, options queue.EnqueueOptions) (queue.Job, bool, error) {
	recorder.job = queue.Job{Version: queue.EnvelopeVersion, ID: "test-job", Kind: kind, Payload: payload, MaxAttempts: options.MaxAttempts}
	return recorder.job, true, nil
}

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

	artifactConfig := sentry.DefaultConfig()
	artifactConfig.ArtifactQuota = 8
	artifactService, _, _, artifactProjects := persistentService(t, artifactConfig)
	if _, err := artifactService.CreateRelease(ctx, artifactProjects[1].ArtifactToken, "web", "v1.2.3", now); err != nil {
		t.Fatal(err)
	}
	artifactPayload := []byte(`{"version":3,"file":"a.js","sources":["a.ts"],"names":[],"mappings":"AAAA"}`)
	if _, err := artifactService.UploadArtifact(ctx, artifactProjects[1].ArtifactToken, "web", "v1.2.3", "a.js.map", artifactPayload, now); !errors.Is(err, sentry.ErrStorageQuota) {
		t.Fatalf("artifact quota err=%v", err)
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

func TestIssueGroupingIsDeterministicPrivateAndStateful(t *testing.T) {
	ctx := context.Background()
	service, pool, _, projects := persistentService(t, sentry.DefaultConfig())
	now := time.Now().UTC()
	for index, message := range []string{"owner@example.test first subject", "another private subject"} {
		eventID := fmt.Sprintf("%032x", index+101)
		payload := []byte(fmt.Sprintf(`{"event_id":%q,"environment":"production","release":"v1.2.3","exception":{"values":[{"type":"RenderError","value":%q,"stacktrace":{"frames":[{"function":"renderInbox","abs_path":"/Users/owner/private/bundle.js","lineno":1,"colno":1}]}}]}}`, eventID, message))
		if _, err := service.Ingest(ctx, sentry.Request{QueryKey: projects[0].PublicKey, Body: envelope(eventID, "event", payload), Now: now.Add(time.Duration(index) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	issues, err := service.ListIssues(ctx, "unresolved", 10)
	if err != nil || len(issues) != 1 || issues[0].EventCount != 2 || issues[0].Title != "RenderError" {
		t.Fatalf("issues=%+v err=%v", issues, err)
	}
	updated, err := service.SetIssueStatus(ctx, issues[0].ID, "ignored")
	if err != nil || updated.Status != "ignored" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	distinctID := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	distinct := []byte(`{"event_id":"` + distinctID + `","environment":"production","exception":{"values":[{"type":"RenderError","stacktrace":{"frames":[{"function":"loadThread","filename":"bundle.js","lineno":1}]}}]}}`)
	if _, err := service.Ingest(ctx, sentry.Request{QueryKey: projects[0].PublicKey, Body: envelope(distinctID, "event", distinct), Now: now}); err != nil {
		t.Fatal(err)
	}
	var issueCount int
	var stacks string
	if err := pool.QueryRow(ctx, `select count(*) from sentry_issues`).Scan(&issueCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select string_agg(normalized_stack::text, ' ') from sentry_events`).Scan(&stacks); err != nil {
		t.Fatal(err)
	}
	if issueCount != 2 || strings.Contains(stacks, "/Users/") || strings.Contains(stacks, "owner@example.test") {
		t.Fatalf("issueCount=%d unsafe stacks=%s", issueCount, stacks)
	}
}

func TestReleaseArtifactsAreAuthorizedQueuedAndSymbolicateEvents(t *testing.T) {
	ctx := context.Background()
	service, pool, store, projects := persistentService(t, sentry.DefaultConfig())
	recorder := &recordingQueue{}
	service.SetQueue(recorder)
	now := time.Now().UTC()
	release, err := service.CreateRelease(ctx, "bearer "+projects[1].ArtifactToken, "web", "v1.2.3", now)
	if err != nil || release.Component != "web" {
		t.Fatalf("release=%+v err=%v", release, err)
	}
	if _, err := service.CreateRelease(ctx, projects[0].ArtifactToken, "web", "v1.2.3", now); !errors.Is(err, sentry.ErrUnauthenticated) {
		t.Fatalf("cross-project auth err=%v", err)
	}
	eventID := "ffffffffffffffffffffffffffffffff"
	event := []byte(`{"event_id":"` + eventID + `","release":"v1.2.3","exception":{"values":[{"type":"WebError","stacktrace":{"frames":[{"filename":"bundle.js","lineno":1,"colno":1}]}}]}}`)
	if _, err := service.Ingest(ctx, sentry.Request{QueryKey: projects[1].PublicKey, Body: envelope(eventID, "event", event), Now: now}); err != nil {
		t.Fatal(err)
	}
	sourceMap := []byte(`{"version":3,"file":"bundle.js","sources":["/private/src/app.ts"],"sourcesContent":["secret"],"names":["renderInbox"],"mappings":"AAAAA"}`)
	artifact, err := service.UploadArtifact(ctx, "Bearer "+projects[1].ArtifactToken, "web", "v1.2.3", "bundle.js.map", sourceMap, now)
	if err != nil || recorder.job.Kind != sentry.ArtifactProcessJobKind || artifact.Status != "pending" {
		t.Fatalf("artifact=%+v job=%+v err=%v", artifact, recorder.job, err)
	}
	processor, err := sentry.NewArtifactProcessor(pool, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Handler()(ctx, recorder.job); err != nil {
		t.Fatal(err)
	}
	var status, stack string
	if err := pool.QueryRow(ctx, `select symbolication_status, normalized_stack::text from sentry_events where event_id=$1`, eventID).Scan(&status, &stack); err != nil {
		t.Fatal(err)
	}
	if status != "resolved" || !strings.Contains(stack, "app.ts") || !strings.Contains(stack, "renderInbox") || strings.Contains(stack, "/private/") {
		t.Fatalf("status=%s stack=%s", status, stack)
	}
	failedArtifact, err := service.UploadArtifact(ctx, projects[1].ArtifactToken, "web", "v1.2.3", "invalid.dif", []byte("not a debug object"), now)
	if err != nil {
		t.Fatal(err)
	}
	processingErr := processor.Handler()(ctx, recorder.job)
	var coded queue.CodedError
	if !errors.As(processingErr, &coded) || coded.JobErrorCode() != "artifact_format_invalid" {
		t.Fatalf("processing error=%v", processingErr)
	}
	var failedStatus string
	var attempts int
	if err := pool.QueryRow(ctx, `select status, attempts from sentry_release_artifacts where id=$1`, failedArtifact.ID).Scan(&failedStatus, &attempts); err != nil {
		t.Fatal(err)
	}
	if failedStatus != "failed" || attempts != 1 {
		t.Fatalf("failed status=%s attempts=%d", failedStatus, attempts)
	}
	cleaned, err := service.Cleanup(ctx, now.Add(sentry.DefaultRetention+time.Hour))
	if err != nil || cleaned != 3 {
		t.Fatalf("cleanup=%d err=%v", cleaned, err)
	}
	var artifacts, objects int
	if err := pool.QueryRow(ctx, `select count(*) from sentry_release_artifacts`).Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from cdn_objects where namespace='sentry'`).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if artifacts != 0 || objects != 0 {
		t.Fatalf("expired artifacts=%d objects=%d", artifacts, objects)
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
