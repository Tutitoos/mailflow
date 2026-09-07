-- name: ExistingSentryEvent :one
SELECT id FROM sentry_events
WHERE component = sqlc.arg(component) AND event_id = sqlc.arg(event_id);

-- name: UpsertSentryIssue :one
INSERT INTO sentry_issues (
  id, fingerprint, component, environment, title, status,
  first_seen_at, last_seen_at, event_count
) VALUES (
  sqlc.arg(id), sqlc.arg(fingerprint), sqlc.arg(component),
  sqlc.arg(environment), sqlc.arg(title), 'unresolved',
  sqlc.arg(seen_at), sqlc.arg(seen_at), 1
)
ON CONFLICT (fingerprint, component, environment) DO UPDATE SET
  last_seen_at = GREATEST(sentry_issues.last_seen_at, EXCLUDED.last_seen_at),
  event_count = sentry_issues.event_count + 1
RETURNING *;

-- name: AttachSentryEventIssue :exec
UPDATE sentry_events SET
  issue_id = sqlc.arg(issue_id),
  grouping_key = sqlc.arg(grouping_key),
  normalized_stack = sqlc.arg(normalized_stack),
  symbolication_status = sqlc.arg(symbolication_status)
WHERE id = sqlc.arg(event_row_id);

-- name: ListSentryIssues :many
SELECT * FROM sentry_issues
WHERE (sqlc.arg(status)::text = '' OR status = sqlc.arg(status))
ORDER BY last_seen_at DESC, id DESC
LIMIT sqlc.arg(query_limit)::bigint;

-- name: SetSentryIssueStatus :one
UPDATE sentry_issues SET status = sqlc.arg(status)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: UpsertSentryRelease :one
INSERT INTO sentry_releases (component, version, created_at)
VALUES (sqlc.arg(component), sqlc.arg(version), sqlc.arg(created_at))
ON CONFLICT (component, version) DO UPDATE SET component = EXCLUDED.component
RETURNING *;

-- name: GetSentryRelease :one
SELECT * FROM sentry_releases
WHERE component = sqlc.arg(component) AND version = sqlc.arg(version);

-- name: SentryArtifactStoredBytes :one
SELECT COALESCE(SUM(size_bytes), 0)::bigint FROM sentry_release_artifacts;

-- name: InsertSentryArtifact :one
INSERT INTO sentry_release_artifacts (
  release_id, object_id, name, kind, checksum_sha256, size_bytes, created_at
) VALUES (
  sqlc.arg(release_id), sqlc.arg(object_id), sqlc.arg(name), sqlc.arg(kind),
  sqlc.arg(checksum_sha256), sqlc.arg(size_bytes), sqlc.arg(created_at)
)
ON CONFLICT (release_id, name) DO NOTHING
RETURNING *;

-- name: GetSentryArtifact :one
SELECT sentry_release_artifacts.*, sentry_releases.component, sentry_releases.version
FROM sentry_release_artifacts
JOIN sentry_releases ON sentry_releases.id = sentry_release_artifacts.release_id
WHERE sentry_release_artifacts.id = sqlc.arg(id);

-- name: GetSentryArtifactByName :one
SELECT sentry_release_artifacts.*, sentry_releases.component, sentry_releases.version
FROM sentry_release_artifacts
JOIN sentry_releases ON sentry_releases.id = sentry_release_artifacts.release_id
WHERE sentry_release_artifacts.release_id = sqlc.arg(release_id)
  AND sentry_release_artifacts.name = sqlc.arg(name);

-- name: MarkSentryArtifactProcessed :exec
UPDATE sentry_release_artifacts SET
  status = 'processed', attempts = attempts + 1, error_code = NULL,
  processed_at = sqlc.arg(processed_at)
WHERE id = sqlc.arg(id);

-- name: MarkSentryArtifactFailed :exec
UPDATE sentry_release_artifacts SET
  status = 'failed', attempts = LEAST(attempts + 1, 20),
  error_code = sqlc.arg(error_code), processed_at = NULL
WHERE id = sqlc.arg(id);

-- name: ListPendingSentryEventsForRelease :many
SELECT id, normalized_stack FROM sentry_events
WHERE component = sqlc.arg(component) AND release = sqlc.arg(version)
  AND symbolication_status IN ('pending', 'failed')
ORDER BY id
LIMIT 10000;

-- name: UpdateSentryEventSymbolication :exec
UPDATE sentry_events SET normalized_stack = sqlc.arg(normalized_stack), symbolication_status = 'resolved'
WHERE id = sqlc.arg(id);

-- name: DeleteSentryArtifact :exec
DELETE FROM sentry_release_artifacts WHERE id = sqlc.arg(id);

-- name: ListExpiredSentryArtifacts :many
SELECT object_id FROM sentry_release_artifacts
WHERE created_at < sqlc.arg(expired_before)
ORDER BY id
LIMIT 10000;

-- name: DeleteExpiredSentryArtifacts :execrows
DELETE FROM sentry_release_artifacts
WHERE created_at < sqlc.arg(expired_before);
