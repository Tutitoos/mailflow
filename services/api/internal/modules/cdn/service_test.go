package cdn

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/google/uuid"
)

func TestAttachmentLifecycleIsOwnerScopedRecoverableAndSentrySafe(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	userID := "0199ed3b-c950-7000-8000-000000000071"
	otherUserID := "0199ed3b-c950-7000-8000-000000000072"
	accountID := "0199ed3b-c950-7000-8000-000000000073"
	if _, err := pool.Exec(ctx, `insert into users (id,email,name) values ($1,'cdn-owner@example.test','Owner')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into accounts (id,user_id,provider,remote_id,display_name,encrypted_credentials,credential_nonce,capabilities) values ($1,$2,'google','cdn-account','Personal',$3,$4,'{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, dbgen.New(pool), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 15, 0, 0, 0, time.UTC)
	attachment, err := service.PutAttachment(ctx, PutAttachmentInput{
		UserID: userID, AccountID: accountID, RecoveryReference: "provider-file-1", Filename: "note.txt",
		MediaType: "text/plain", Source: strings.NewReader("mailflow"), Now: now,
	})
	if err != nil || attachment.SizeBytes != 8 || attachment.ETag == "" {
		t.Fatalf("put attachment = %+v, %v", attachment, err)
	}
	if _, _, err := service.OpenAttachment(ctx, otherUserID, attachment.ObjectID, now); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("cross-owner open error = %v", err)
	}
	loaded, file, err := service.OpenAttachment(ctx, userID, attachment.ObjectID, now)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(file)
	_ = file.Close()
	if string(content) != "mailflow" || loaded.RecoveryReference == nil || *loaded.RecoveryReference != "provider-file-1" {
		t.Fatalf("loaded attachment = %+v content=%q", loaded, content)
	}

	sentryID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := pool.Exec(ctx, `insert into cdn_objects (object_id,namespace,media_type,size_bytes,etag,storage_status) values ($1,'sentry','application/octet-stream',1,$2,'missing')`, sentryID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	result, err := service.Cleanup(ctx, now.Add(2*time.Hour))
	if err != nil || result.Expired != 1 {
		t.Fatalf("expiry cleanup = %+v, %v", result, err)
	}
	if _, _, err := service.OpenAttachment(ctx, userID, attachment.ObjectID, now.Add(2*time.Hour)); !errors.Is(err, ErrAttachmentMissing) {
		t.Fatalf("expired open error = %v", err)
	}
	var status, recovery string
	if err := pool.QueryRow(ctx, `select storage_status,recovery_reference from cdn_objects where object_id=$1`, attachment.ObjectID).Scan(&status, &recovery); err != nil || status != "missing" || recovery != "provider-file-1" {
		t.Fatalf("recoverable metadata = status %q reference %q error %v", status, recovery, err)
	}
	result, err = service.Cleanup(ctx, now.Add(2*time.Hour+OrphanGrace+time.Second))
	if err != nil || result.Orphans != 1 {
		t.Fatalf("orphan cleanup = %+v, %v", result, err)
	}
	var sentryCount int
	if err := pool.QueryRow(ctx, `select count(*) from cdn_objects where object_id=$1`, sentryID).Scan(&sentryCount); err != nil || sentryCount != 1 {
		t.Fatalf("Sentry metadata count = %d, %v", sentryCount, err)
	}
}

func TestAttachmentInputRejectsHeaderInjectionAndUnknownAccount(t *testing.T) {
	service, _, userID, accountID := serviceFixture(t)
	input := PutAttachmentInput{UserID: userID, AccountID: accountID, Filename: "bad\r\nheader", MediaType: "text/plain", Source: strings.NewReader("safe")}
	if _, err := service.PutAttachment(context.Background(), input); !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("header injection error = %v", err)
	}
	input.Filename = "safe.txt"
	input.AccountID = uuid.NewString()
	if _, err := service.PutAttachment(context.Background(), input); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("unknown account error = %v", err)
	}
}

func serviceFixture(t *testing.T) (*Service, string, string, string) {
	t.Helper()
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	userID, accountID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into users (id,email,name) values ($1,'fixture@example.test','Owner')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into accounts (id,user_id,provider,remote_id,display_name,encrypted_credentials,credential_nonce,capabilities) values ($1,$2,'google','fixture','Personal',$3,$4,'{}')`, accountID, userID, []byte{1}, []byte{2}); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, dbgen.New(pool), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return service, databaseURL, userID, accountID
}
