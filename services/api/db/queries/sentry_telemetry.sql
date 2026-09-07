-- name: InsertSentryTrace :one
INSERT INTO sentry_traces (
  event_id, component, trace_id, span_id, parent_span_id, operation, status,
  started_at, duration_ms, span_count, received_at
) VALUES (
  sqlc.arg(event_id), sqlc.arg(component), sqlc.arg(trace_id), sqlc.arg(span_id),
  sqlc.narg(parent_span_id), sqlc.narg(operation), sqlc.narg(status),
  sqlc.narg(started_at), sqlc.narg(duration_ms), sqlc.arg(span_count), sqlc.arg(received_at)
)
RETURNING id;

-- name: InsertSentrySpan :exec
INSERT INTO sentry_spans (
  trace_row_id, trace_id, span_id, parent_span_id, operation, status, started_at, duration_ms
) VALUES (
  sqlc.arg(trace_row_id), sqlc.arg(trace_id), sqlc.arg(span_id), sqlc.narg(parent_span_id),
  sqlc.narg(operation), sqlc.narg(status), sqlc.narg(started_at), sqlc.narg(duration_ms)
)
ON CONFLICT (trace_row_id, span_id) DO NOTHING;

-- name: InsertSentryProfile :exec
INSERT INTO sentry_profiles (event_id, component, platform, sample_count, frame_count, received_at)
VALUES (sqlc.arg(event_id), sqlc.arg(component), sqlc.narg(platform), sqlc.arg(sample_count), sqlc.arg(frame_count), sqlc.arg(received_at))
ON CONFLICT (event_id) DO NOTHING;

-- name: UpsertSentryReplay :one
INSERT INTO sentry_replays (component, replay_id, environment, first_seen_at, last_seen_at)
VALUES (sqlc.arg(component), sqlc.arg(replay_id), sqlc.arg(environment), sqlc.arg(seen_at), sqlc.arg(seen_at))
ON CONFLICT (component, replay_id) DO UPDATE SET
  last_seen_at = GREATEST(sentry_replays.last_seen_at, EXCLUDED.last_seen_at)
RETURNING id;

-- name: InsertSentryReplaySegment :execrows
INSERT INTO sentry_replay_segments (
  replay_row_id, event_id, sequence, object_id, size_bytes, checksum_sha256, received_at
) VALUES (
  sqlc.arg(replay_row_id), sqlc.arg(event_id), sqlc.arg(sequence), sqlc.arg(object_id),
  sqlc.arg(size_bytes), sqlc.arg(checksum_sha256), sqlc.arg(received_at)
)
ON CONFLICT (replay_row_id, sequence) DO NOTHING;

-- name: IncrementSentryReplaySegments :exec
UPDATE sentry_replays SET segment_count = segment_count + 1 WHERE id = sqlc.arg(id);

-- name: SentryReplayStoredBytes :one
SELECT COALESCE(SUM(size_bytes), 0)::bigint FROM sentry_replay_segments;

-- name: SentryTelemetrySummary :one
SELECT
  (SELECT count(*) FROM sentry_traces WHERE sentry_traces.received_at >= sqlc.arg(since))::bigint AS traces,
  (SELECT count(*) FROM sentry_spans JOIN sentry_traces ON sentry_traces.id=sentry_spans.trace_row_id WHERE sentry_traces.received_at >= sqlc.arg(since))::bigint AS spans,
  (SELECT count(*) FROM sentry_profiles WHERE sentry_profiles.received_at >= sqlc.arg(since))::bigint AS profiles,
  (SELECT count(*) FROM sentry_replays WHERE sentry_replays.last_seen_at >= sqlc.arg(since))::bigint AS replays,
  (SELECT count(*) FROM sentry_replay_segments WHERE sentry_replay_segments.received_at >= sqlc.arg(since))::bigint AS replay_segments;

-- name: ListExpiredSentryReplayObjects :many
SELECT object_id FROM sentry_replay_segments WHERE received_at < sqlc.arg(expired_before);

-- name: DeleteExpiredSentryReplaySegments :execrows
DELETE FROM sentry_replay_segments WHERE received_at < sqlc.arg(expired_before);

-- name: RefreshSentryReplaySegmentCounts :exec
UPDATE sentry_replays
SET segment_count = (
  SELECT count(*)::integer
  FROM sentry_replay_segments
  WHERE sentry_replay_segments.replay_row_id = sentry_replays.id
);

-- name: DeleteExpiredSentryReplays :execrows
DELETE FROM sentry_replays WHERE last_seen_at < sqlc.arg(expired_before);

-- name: DeleteExpiredSentryTraces :execrows
DELETE FROM sentry_traces WHERE received_at < sqlc.arg(expired_before);

-- name: DeleteExpiredSentryProfiles :execrows
DELETE FROM sentry_profiles WHERE received_at < sqlc.arg(expired_before);
