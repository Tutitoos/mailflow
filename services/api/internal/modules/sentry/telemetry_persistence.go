package sentry

import (
	"context"
	"fmt"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/jackc/pgx/v5/pgtype"
)

func (service *Service) persistTraceAndProfile(ctx context.Context, queries *dbgen.Queries, eventID int64, component string, envelope parsedEnvelope, now time.Time) error {
	if envelope.trace != nil && sampled(envelope.eventID, "trace", service.config.TraceSampleRate) {
		trace := envelope.trace
		traceRowID, err := queries.InsertSentryTrace(ctx, dbgen.InsertSentryTraceParams{
			EventID: eventID, Component: component, TraceID: trace.TraceID, SpanID: trace.SpanID,
			ParentSpanID: optionalText(trace.ParentSpanID), Operation: optionalText(trace.Operation), Status: optionalText(trace.Status),
			StartedAt: optionalTimestamp(trace.StartedAt), DurationMs: optionalFloat(trace.DurationMS), SpanCount: int32(len(trace.Spans)), ReceivedAt: timestamp(now),
		})
		if err != nil {
			return fmt.Errorf("persist Sentry trace: %w", err)
		}
		for _, span := range trace.Spans {
			if err := queries.InsertSentrySpan(ctx, dbgen.InsertSentrySpanParams{
				TraceRowID: traceRowID, TraceID: span.TraceID, SpanID: span.SpanID,
				ParentSpanID: optionalText(span.ParentSpanID), Operation: optionalText(span.Operation), Status: optionalText(span.Status),
				StartedAt: optionalTimestamp(span.StartedAt), DurationMs: optionalFloat(span.DurationMS),
			}); err != nil {
				return fmt.Errorf("persist Sentry span: %w", err)
			}
		}
	}
	if envelope.profile != nil && sampled(envelope.eventID, "profile", service.config.ProfileSampleRate) {
		profile := envelope.profile
		if err := queries.InsertSentryProfile(ctx, dbgen.InsertSentryProfileParams{
			EventID: eventID, Component: component, Platform: optionalText(profile.Platform), SampleCount: int32(profile.SampleCount), FrameCount: int32(profile.FrameCount), ReceivedAt: timestamp(now),
		}); err != nil {
			return fmt.Errorf("persist Sentry profile: %w", err)
		}
	}
	return nil
}

func optionalTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: !value.IsZero()}
}

func optionalFloat(value float64) pgtype.Float8 {
	return pgtype.Float8{Float64: value, Valid: value > 0}
}

func replayEnvironment(value string) string {
	if value == "" {
		return "default"
	}
	return value
}
