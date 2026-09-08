package cdn

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
	"github.com/google/uuid"
)

type recoveryProvider struct {
	calls atomic.Int32
}

func (provider *recoveryProvider) DownloadAttachment(ctx context.Context, _, _ string) (io.ReadCloser, error) {
	provider.calls.Add(1)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return io.NopCloser(strings.NewReader("mailflow")), nil
	}
}

type recoveryResolver struct{ provider *recoveryProvider }

func (resolver recoveryResolver) ResolveAttachmentProvider(context.Context, string, string) (AttachmentProvider, error) {
	return resolver.provider, nil
}

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

func TestMicrosoftAttachmentRecoveryCachesRenewsCancelsAndKeepsDomainIdentity(t *testing.T) {
	_, databaseURL, userID, accountID := serviceFixture(t)
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `update accounts set provider = 'microsoft' where id = $1`, accountID); err != nil {
		t.Fatal(err)
	}
	threadID, messageID, attachmentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into threads (id,account_id,remote_id,last_message_at) values ($1,$2,'thread',now())`, threadID, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into messages (id,thread_id,account_id,remote_id,sender,recipients,sent_at) values ($1,$2,$3,'graph-message','{}','[]',now())`, messageID, threadID, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into message_attachments (id,message_id,account_id,position,remote_id,filename,media_type,disposition,size_bytes) values ($1,$2,$3,0,'graph-attachment','fixture.txt','text/plain','attachment',8)`, attachmentID, messageID, accountID); err != nil {
		t.Fatal(err)
	}
	provider := &recoveryProvider{}
	store, err := NewStore(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, dbgen.New(pool), time.Hour, recoveryResolver{provider})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, file, openErr := service.OpenMessageAttachment(ctx, userID, attachmentID, now)
			if openErr == nil {
				content, readErr := io.ReadAll(file)
				_ = file.Close()
				if readErr != nil || string(content) != "mailflow" {
					openErr = errors.New("unexpected recovered content")
				}
			}
			errorsFound <- openErr
		}()
	}
	wait.Wait()
	close(errorsFound)
	for openErr := range errorsFound {
		if openErr != nil {
			t.Fatal(openErr)
		}
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls after concurrent miss = %d", provider.calls.Load())
	}
	if _, err := service.Cleanup(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, renewed, err := service.OpenMessageAttachment(ctx, userID, attachmentID, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_ = renewed.Close()
	if provider.calls.Load() != 2 {
		t.Fatalf("provider calls after expiry = %d", provider.calls.Load())
	}
	if _, _, err := service.OpenMessageAttachment(ctx, uuid.NewString(), attachmentID, now); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("cross-owner recovery error = %v", err)
	}

	cancelledID := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into message_attachments (id,message_id,account_id,position,remote_id,filename,media_type,disposition,size_bytes) values ($1,$2,$3,1,'cancel-part','cancel.txt','text/plain','attachment',8)`, cancelledID, messageID, accountID); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := service.OpenMessageAttachment(cancelled, userID, cancelledID, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled recovery error = %v", err)
	}
	oversizedID := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into message_attachments (id,message_id,account_id,position,remote_id,filename,media_type,disposition,size_bytes) values ($1,$2,$3,2,'large-part','large.txt','text/plain','attachment',2048)`, oversizedID, messageID, accountID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.OpenMessageAttachment(ctx, userID, oversizedID, now); !errors.Is(err, ErrAttachmentUnavailable) {
		t.Fatalf("oversized recovery error = %v", err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("provider was called before enforcing size limit: %d", provider.calls.Load())
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
