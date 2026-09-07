-- name: UpsertSentryProject :exec
INSERT INTO sentry_projects (component, public_key, artifact_token_hash, updated_at)
VALUES (sqlc.arg(component), sqlc.arg(public_key), sqlc.arg(artifact_token_hash), sqlc.arg(updated_at))
ON CONFLICT (component) DO UPDATE SET
  public_key = EXCLUDED.public_key,
  artifact_token_hash = EXCLUDED.artifact_token_hash,
  enabled = true,
  updated_at = EXCLUDED.updated_at;

-- name: GetSentryProjectByKey :one
SELECT component, public_key, enabled FROM sentry_projects
WHERE public_key = sqlc.arg(public_key) AND enabled = true;

-- name: GetSentryProjectByArtifactToken :one
SELECT component, enabled FROM sentry_projects
WHERE artifact_token_hash = sqlc.arg(artifact_token_hash) AND enabled = true;

-- name: InsertSentryEvent :one
INSERT INTO sentry_events (
  event_id, component, event_type, environment, release, level, sdk_name,
  received_bytes, item_count, received_at
) VALUES (
  sqlc.arg(event_id), sqlc.arg(component), sqlc.arg(event_type),
  sqlc.narg(environment), sqlc.narg(release), sqlc.narg(level),
  sqlc.narg(sdk_name), sqlc.arg(received_bytes), sqlc.arg(item_count),
  sqlc.arg(received_at)
)
ON CONFLICT (component, event_id) DO NOTHING
RETURNING id;

-- name: InsertSentryEventItem :exec
INSERT INTO sentry_event_items (
  event_id, item_type, content_type, received_bytes, payload_sha256,
  summary, payload_object_id, discarded
) VALUES (
  sqlc.arg(event_id), sqlc.arg(item_type), sqlc.narg(content_type),
  sqlc.arg(received_bytes), sqlc.arg(payload_sha256), sqlc.arg(summary),
  sqlc.narg(payload_object_id), sqlc.arg(discarded)
);

-- name: InsertSentryCDNObject :exec
INSERT INTO cdn_objects (
  object_id, namespace, media_type, size_bytes, etag, storage_status,
  stored_at, updated_at
) VALUES (
  sqlc.arg(object_id), 'sentry', sqlc.arg(media_type), sqlc.arg(size_bytes),
  sqlc.arg(etag), 'cached', sqlc.arg(stored_at), sqlc.arg(stored_at)
);

-- name: SentryStoredBytes :one
SELECT COALESCE(SUM(received_bytes), 0)::bigint FROM sentry_events;

-- name: DeleteExpiredSentryEvents :execrows
DELETE FROM sentry_events WHERE received_at < sqlc.arg(expired_before);

-- name: ListExpiredSentryObjects :many
SELECT DISTINCT sentry_event_items.payload_object_id
FROM sentry_event_items
JOIN sentry_events ON sentry_events.id = sentry_event_items.event_id
WHERE sentry_events.received_at < sqlc.arg(expired_before)
  AND sentry_event_items.payload_object_id IS NOT NULL;

-- name: DeleteSentryCDNObject :exec
DELETE FROM cdn_objects
WHERE object_id = sqlc.arg(object_id) AND namespace = 'sentry';
