package cdn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrAttachmentNotFound    = errors.New("attachment not found")
	ErrAttachmentMissing     = errors.New("attachment cache entry needs provider recovery")
	ErrAttachmentUnavailable = errors.New("attachment provider is unavailable")
	ErrInvalidAttachment     = errors.New("invalid attachment")
)

const (
	DefaultRetention = 30 * 24 * time.Hour
	DefaultBatchSize = 100
	OrphanGrace      = 24 * time.Hour
)

type Attachment struct {
	ObjectID          string
	AccountID         string
	RecoveryReference *string
	Filename          *string
	MediaType         string
	SizeBytes         int64
	ETag              string
	ExpiresAt         time.Time
}

type PutAttachmentInput struct {
	UserID            string
	AccountID         string
	RecoveryReference string
	Filename          string
	MediaType         string
	Source            io.Reader
	Now               time.Time
}

type CleanupResult struct {
	Expired int
	Orphans int
}

type CleanupObserver func(CleanupResult, error)

type AttachmentProvider interface {
	DownloadAttachment(context.Context, string, string) (io.ReadCloser, error)
}

type AttachmentProviderResolver interface {
	ResolveAttachmentProvider(context.Context, string, string) (AttachmentProvider, error)
}

type Service struct {
	store     *Store
	queries   *dbgen.Queries
	retention time.Duration
	resolver  AttachmentProviderResolver
	locks     sync.Map
}

func NewService(store *Store, queries *dbgen.Queries, retention time.Duration, resolvers ...AttachmentProviderResolver) (*Service, error) {
	if store == nil || queries == nil || retention <= 0 {
		return nil, errors.New("CDN service requires storage, database, and positive retention")
	}
	var resolver AttachmentProviderResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	return &Service{store: store, queries: queries, retention: retention, resolver: resolver}, nil
}

func (service *Service) MaxAttachmentBytes() int64 { return service.store.maxBytes }

func (service *Service) PutAttachment(ctx context.Context, input PutAttachmentInput) (Attachment, error) {
	userID, accountID, err := scopedIDs(input.UserID, input.AccountID)
	if err != nil || input.Source == nil || !validMetadata(input.Filename, input.RecoveryReference) {
		return Attachment{}, ErrInvalidAttachment
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	objectID, err := ids.New()
	if err != nil {
		return Attachment{}, fmt.Errorf("create attachment ID: %w", err)
	}
	objectID, _ = normalizeID(objectID)
	info, err := service.store.PutValidated("attachments", objectID, input.MediaType, input.Source)
	if err != nil {
		return Attachment{}, err
	}
	row, err := service.queries.UpsertAttachmentObject(ctx, dbgen.UpsertAttachmentObjectParams{
		ObjectID: objectID, RecoveryReference: optionalText(input.RecoveryReference), Filename: optionalText(input.Filename),
		MediaType: info.MediaType, SizeBytes: info.Size, Etag: info.ETag,
		ExpiresAt: timestamp(now.Add(service.retention)), StoredAt: timestamp(now), AccountID: accountID, UserID: userID,
	})
	if err != nil {
		_ = service.store.Remove("attachments", objectID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Attachment{}, ErrAttachmentNotFound
		}
		return Attachment{}, fmt.Errorf("persist attachment metadata: %w", err)
	}
	return mapAttachment(row), nil
}

func (service *Service) OpenAttachment(ctx context.Context, user, object string, now time.Time) (Attachment, *os.File, error) {
	userID, err := parseID(user)
	objectID, objectErr := normalizeID(object)
	if err != nil || objectErr != nil {
		return Attachment{}, nil, ErrAttachmentNotFound
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	row, err := service.queries.GetAttachmentObjectForUser(ctx, dbgen.GetAttachmentObjectForUserParams{ObjectID: objectID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, nil, ErrAttachmentNotFound
	}
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("authorize attachment: %w", err)
	}
	attachment := mapAttachment(row)
	if row.StorageStatus != "cached" || (row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(now)) {
		if row.StorageStatus == "cached" {
			_ = service.queries.MarkAttachmentObjectMissing(ctx, dbgen.MarkAttachmentObjectMissingParams{UpdatedAt: timestamp(now.UTC()), ObjectID: objectID})
			_ = service.store.Remove("attachments", objectID)
		}
		return attachment, nil, ErrAttachmentMissing
	}
	file, err := service.store.Open("attachments", objectID)
	if errors.Is(err, os.ErrNotExist) {
		_ = service.queries.MarkAttachmentObjectMissing(ctx, dbgen.MarkAttachmentObjectMissingParams{UpdatedAt: timestamp(now.UTC()), ObjectID: objectID})
		return attachment, nil, ErrAttachmentMissing
	}
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("open attachment: %w", err)
	}
	if err := service.queries.TouchAttachmentObject(ctx, dbgen.TouchAttachmentObjectParams{AccessedAt: timestamp(now.UTC()), ExpiresAt: timestamp(now.UTC().Add(service.retention)), ObjectID: objectID}); err != nil {
		_ = file.Close()
		return Attachment{}, nil, fmt.Errorf("touch attachment: %w", err)
	}
	return attachment, file, nil
}

// OpenMessageAttachment keeps the stable domain attachment ID at the HTTP
// boundary while transparently filling or renewing its local cache object.
func (service *Service) OpenMessageAttachment(ctx context.Context, user, attachmentID string, now time.Time) (Attachment, *os.File, error) {
	userID, err := parseID(user)
	domainID, attachmentErr := parseID(attachmentID)
	if err != nil || attachmentErr != nil {
		return Attachment{}, nil, ErrAttachmentNotFound
	}
	row, err := service.queries.GetMessageAttachmentForUser(ctx, dbgen.GetMessageAttachmentForUserParams{AttachmentID: domainID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, nil, ErrAttachmentNotFound
	}
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("authorize message attachment: %w", err)
	}
	if row.CachedObjectID.Valid {
		cached, file, openErr := service.OpenAttachment(ctx, user, row.CachedObjectID.String, now)
		if openErr == nil || !errors.Is(openErr, ErrAttachmentMissing) {
			return cached, file, openErr
		}
	}

	lockValue, _ := service.locks.LoadOrStore(uuid.UUID(domainID.Bytes).String(), &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	row, err = service.queries.GetMessageAttachmentForUser(ctx, dbgen.GetMessageAttachmentForUserParams{AttachmentID: domainID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, nil, ErrAttachmentNotFound
	}
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("authorize message attachment: %w", err)
	}
	if row.CachedObjectID.Valid {
		cached, file, openErr := service.OpenAttachment(ctx, user, row.CachedObjectID.String, now)
		if openErr == nil || !errors.Is(openErr, ErrAttachmentMissing) {
			return cached, file, openErr
		}
	}
	if service.resolver == nil || row.Provider != "google" || !row.RemoteID.Valid || row.MessageRemoteID == "" || row.SizeBytes > service.store.maxBytes {
		return Attachment{}, nil, ErrAttachmentUnavailable
	}
	accountID := uuid.UUID(row.AccountID.Bytes).String()
	provider, err := service.resolver.ResolveAttachmentProvider(ctx, user, accountID)
	if err != nil {
		return Attachment{}, nil, ErrAttachmentUnavailable
	}
	remote, err := provider.DownloadAttachment(ctx, row.MessageRemoteID, row.RemoteID.String)
	if err != nil {
		return Attachment{}, nil, ErrAttachmentUnavailable
	}
	defer remote.Close()
	filename := ""
	if row.Filename.Valid {
		filename = row.Filename.String
	}
	stored, err := service.PutAttachment(ctx, PutAttachmentInput{
		UserID: user, AccountID: accountID,
		RecoveryReference: row.MessageRemoteID + ":" + row.RemoteID.String,
		Filename:          filename, MediaType: row.MediaType,
		Source: &contextReader{ctx: ctx, source: remote}, Now: now,
	})
	if err != nil {
		return Attachment{}, nil, err
	}
	linked, err := service.queries.LinkMessageAttachmentObject(ctx, dbgen.LinkMessageAttachmentObjectParams{ObjectID: pgtype.Text{String: stored.ObjectID, Valid: true}, AttachmentID: domainID, UserID: userID})
	if err != nil || linked != 1 {
		_ = service.store.Remove("attachments", stored.ObjectID)
		return Attachment{}, nil, ErrAttachmentNotFound
	}
	return service.OpenAttachment(ctx, user, stored.ObjectID, now)
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader *contextReader) Read(destination []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.source.Read(destination)
	}
}

func (service *Service) Cleanup(ctx context.Context, now time.Time) (CleanupResult, error) {
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := CleanupResult{}
	expired, err := service.queries.ListExpiredCachedAttachmentObjects(ctx, dbgen.ListExpiredCachedAttachmentObjectsParams{ExpiredAt: timestamp(now), BatchSize: DefaultBatchSize})
	if err != nil {
		return result, fmt.Errorf("list expired attachments: %w", err)
	}
	for _, object := range expired {
		if err := service.queries.MarkAttachmentObjectMissing(ctx, dbgen.MarkAttachmentObjectMissingParams{UpdatedAt: timestamp(now), ObjectID: object.ObjectID}); err != nil {
			return result, fmt.Errorf("expire attachment metadata: %w", err)
		}
		if err := service.store.Remove("attachments", object.ObjectID); err != nil {
			return result, err
		}
		result.Expired++
	}
	orphans, err := service.queries.ListOrphanedAttachmentObjects(ctx, dbgen.ListOrphanedAttachmentObjectsParams{OrphanedBefore: timestamp(now.Add(-OrphanGrace)), BatchSize: DefaultBatchSize})
	if err != nil {
		return result, fmt.Errorf("list orphaned attachments: %w", err)
	}
	for _, object := range orphans {
		deleted, err := service.queries.DeleteOrphanedAttachmentObject(ctx, object.ObjectID)
		if err != nil {
			return result, fmt.Errorf("delete orphaned attachment: %w", err)
		}
		if deleted == 1 {
			result.Orphans++
		}
	}
	return result, nil
}

func (service *Service) RunCleanupLoop(ctx context.Context, interval time.Duration, observer CleanupObserver) error {
	if interval <= 0 {
		return errors.New("CDN cleanup interval must be positive")
	}
	run := func() {
		result, err := service.Cleanup(ctx, time.Now().UTC())
		if observer != nil {
			observer(result, err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			run()
		}
	}
}

func validMetadata(filename, recovery string) bool {
	return utf8.ValidString(filename) && utf8.ValidString(recovery) && len(strings.TrimSpace(filename)) <= 1024 && len(strings.TrimSpace(recovery)) <= 2048 && !strings.ContainsAny(filename, "\r\n\x00")
}

func parseID(value string) (pgtype.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func scopedIDs(user, account string) (pgtype.UUID, pgtype.UUID, error) {
	userID, err := parseID(user)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	accountID, err := parseID(account)
	return userID, accountID, err
}

func optionalText(value string) pgtype.Text {
	trimmed := strings.TrimSpace(value)
	return pgtype.Text{String: trimmed, Valid: trimmed != ""}
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func textPointer(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func mapAttachment(row dbgen.CdnObject) Attachment {
	return Attachment{
		ObjectID: row.ObjectID, AccountID: uuid.UUID(row.AccountID.Bytes).String(), RecoveryReference: textPointer(row.RecoveryReference),
		Filename: textPointer(row.Filename), MediaType: row.MediaType, SizeBytes: row.SizeBytes, ETag: row.Etag, ExpiresAt: row.ExpiresAt.Time.UTC(),
	}
}
