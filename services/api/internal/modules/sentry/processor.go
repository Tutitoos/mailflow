package sentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ArtifactProcessor struct {
	queries *dbgen.Queries
	cdn     *cdn.Store
}

type artifactJobPayload struct {
	ArtifactID int64 `json:"artifactId"`
}

type artifactJobError string

func (err artifactJobError) Error() string        { return string(err) }
func (err artifactJobError) JobErrorCode() string { return string(err) }

func NewArtifactProcessor(pool *pgxpool.Pool, store *cdn.Store) (*ArtifactProcessor, error) {
	if pool == nil || store == nil {
		return nil, errors.New("sentry artifact processor requires database and CDN storage")
	}
	return &ArtifactProcessor{queries: dbgen.New(pool), cdn: store}, nil
}

func (processor *ArtifactProcessor) Handler() queue.Handler {
	return func(ctx context.Context, job queue.Job) error {
		var payload artifactJobPayload
		if json.Unmarshal(job.Payload, &payload) != nil || payload.ArtifactID <= 0 {
			return artifactJobError("artifact_payload_invalid")
		}
		artifact, err := processor.queries.GetSentryArtifact(ctx, payload.ArtifactID)
		if err != nil {
			return artifactJobError("artifact_not_found")
		}
		file, err := processor.cdn.Open("sentry", artifact.ObjectID)
		if err != nil {
			return processor.fail(ctx, artifact.ID, "artifact_object_missing")
		}
		defer file.Close()
		content, err := io.ReadAll(io.LimitReader(file, MaxArtifactBytes+1))
		if err != nil || len(content) == 0 || len(content) > MaxArtifactBytes {
			return processor.fail(ctx, artifact.ID, "artifact_read_invalid")
		}
		symbolizer, err := newArtifactSymbolizer(artifact.Kind, artifact.Name, content)
		if err != nil {
			return processor.fail(ctx, artifact.ID, "artifact_format_invalid")
		}
		events, err := processor.queries.ListPendingSentryEventsForRelease(ctx, dbgen.ListPendingSentryEventsForReleaseParams{Component: artifact.Component, Version: pgtype.Text{String: artifact.Version, Valid: true}})
		if err != nil {
			return fmt.Errorf("list events for symbolication: %w", err)
		}
		for _, event := range events {
			var frames []normalizedFrame
			if json.Unmarshal(event.NormalizedStack, &frames) != nil {
				continue
			}
			changed := false
			for index := range frames {
				if symbolizer(&frames[index]) {
					changed = true
				}
			}
			if !changed {
				continue
			}
			stack, marshalErr := json.Marshal(frames)
			if marshalErr != nil {
				return fmt.Errorf("marshal symbolicated stack: %w", marshalErr)
			}
			if err := processor.queries.UpdateSentryEventSymbolication(ctx, dbgen.UpdateSentryEventSymbolicationParams{NormalizedStack: stack, ID: event.ID}); err != nil {
				return fmt.Errorf("update symbolicated event: %w", err)
			}
		}
		if err := processor.queries.MarkSentryArtifactProcessed(ctx, dbgen.MarkSentryArtifactProcessedParams{ProcessedAt: timestamp(time.Now()), ID: artifact.ID}); err != nil {
			return fmt.Errorf("mark sentry artifact processed: %w", err)
		}
		return nil
	}
}

func (processor *ArtifactProcessor) fail(ctx context.Context, id int64, code string) error {
	if err := processor.queries.MarkSentryArtifactFailed(ctx, dbgen.MarkSentryArtifactFailedParams{ErrorCode: pgtype.Text{String: code, Valid: true}, ID: id}); err != nil {
		return fmt.Errorf("mark sentry artifact failed: %w", err)
	}
	return artifactJobError(code)
}
