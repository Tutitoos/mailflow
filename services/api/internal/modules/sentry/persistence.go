package sentry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	if _, err := queries.ExistingSentryEvent(ctx, dbgen.ExistingSentryEventParams{Component: component, EventID: envelope.eventID}); err == nil {
		return ErrDuplicate
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check sentry event: %w", err)
	}
	issueID := pgtype.UUID{}
	groupingKey := ""
	if envelope.groupingSeed != "" {
		environment := envelope.environment
		if environment == "" {
			environment = "default"
		}
		groupingKey = digest([]byte(component + "\x00" + environment + "\x00" + envelope.groupingSeed))
		generatedID, idErr := ids.New()
		if idErr != nil {
			return fmt.Errorf("create sentry issue ID: %w", idErr)
		}
		parsedID, idErr := uuid.Parse(generatedID)
		if idErr != nil {
			return fmt.Errorf("parse sentry issue ID: %w", idErr)
		}
		issue, issueErr := queries.UpsertSentryIssue(ctx, dbgen.UpsertSentryIssueParams{
			ID: pgtype.UUID{Bytes: parsedID, Valid: true}, Fingerprint: groupingKey,
			Component: component, Environment: environment, Title: envelope.issueTitle, SeenAt: timestamp(now),
		})
		if issueErr != nil {
			return fmt.Errorf("group sentry issue: %w", issueErr)
		}
		issueID = issue.ID
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
	stack, _ := json.Marshal(envelope.stack)
	symbolicationStatus := "not_required"
	if len(envelope.stack) > 0 && envelope.release != "" {
		symbolicationStatus = "pending"
	}
	if err := queries.AttachSentryEventIssue(ctx, dbgen.AttachSentryEventIssueParams{
		IssueID: issueID, GroupingKey: optionalText(groupingKey), NormalizedStack: stack,
		SymbolicationStatus: symbolicationStatus, EventRowID: eventID,
	}); err != nil {
		return fmt.Errorf("attach sentry issue: %w", err)
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
