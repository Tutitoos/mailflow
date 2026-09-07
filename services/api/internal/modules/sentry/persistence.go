package sentry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/jackc/pgx/v5"
)

func (service *Service) persist(ctx context.Context, component string, envelope parsedEnvelope, receivedBytes int64, now time.Time) error {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin sentry ingestion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := service.queries.WithTx(tx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('mailflow:sentry:quota'))`); err != nil {
		return fmt.Errorf("lock sentry quota: %w", err)
	}
	eventID, err := queries.InsertSentryEvent(ctx, dbgen.InsertSentryEventParams{
		EventID: envelope.eventID, Component: component, EventType: envelope.eventType,
		Environment: optionalText(envelope.environment), Release: optionalText(envelope.release),
		Level: optionalText(envelope.level), SdkName: optionalText(envelope.sdkName),
		ReceivedBytes: receivedBytes, ItemCount: int64(len(envelope.items)), ReceivedAt: timestamp(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("persist sentry event: %w", err)
	}
	storedBytes, err := queries.SentryStoredBytes(ctx)
	if err != nil {
		return fmt.Errorf("read sentry quota: %w", err)
	}
	if storedBytes > service.config.StorageQuota {
		return ErrStorageQuota
	}
	createdObjects := make([]string, 0, len(envelope.items))
	committed := false
	defer func() {
		if !committed {
			for _, objectID := range createdObjects {
				_ = service.cdn.Remove("sentry", objectID)
			}
		}
	}()
	for _, item := range envelope.items {
		objectID := ""
		if len(item.payload) >= largePayloadThreshold && !item.discarded {
			objectID, err = newEventID()
			if err != nil {
				return fmt.Errorf("create sentry object ID: %w", err)
			}
			info, putErr := service.cdn.PutValidated("sentry", objectID, "application/octet-stream", bytes.NewReader(item.summary))
			if putErr != nil {
				return fmt.Errorf("store sentry summary: %w", putErr)
			}
			createdObjects = append(createdObjects, objectID)
			if err := queries.InsertSentryCDNObject(ctx, dbgen.InsertSentryCDNObjectParams{ObjectID: objectID, MediaType: info.MediaType, SizeBytes: info.Size, Etag: info.ETag, StoredAt: timestamp(now)}); err != nil {
				return fmt.Errorf("persist sentry object: %w", err)
			}
		}
		if err := queries.InsertSentryEventItem(ctx, dbgen.InsertSentryEventItemParams{
			EventID: eventID, ItemType: item.typeName, ContentType: optionalText(item.contentType),
			ReceivedBytes: int64(len(item.payload)), PayloadSha256: digest(item.payload), Summary: item.summary,
			PayloadObjectID: optionalText(objectID), Discarded: item.discarded,
		}); err != nil {
			return fmt.Errorf("persist sentry item: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit sentry ingestion: %w", err)
	}
	committed = true
	return nil
}
