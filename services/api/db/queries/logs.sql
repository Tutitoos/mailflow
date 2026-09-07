-- name: InsertLogEntry :one
INSERT INTO log_entries (occurred_at, service, module, level, event, request_id, attributes)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, occurred_at, service, module, level, event, request_id, attributes;

-- name: ListLogEntries :many
SELECT id, occurred_at, service, module, level, event, request_id, attributes
FROM log_entries
WHERE occurred_at >= $1 AND occurred_at < $2
  AND ($3::text = '' OR service = $3)
  AND ($4::text = '' OR module = $4)
  AND ($5::text = '' OR level = $5)
  AND ($6::text = '' OR event = $6)
  AND ($7::text = '' OR request_id = $7)
ORDER BY occurred_at DESC, id DESC
LIMIT $8;

-- name: DeleteExpiredLogEntries :execrows
DELETE FROM log_entries WHERE occurred_at < $1;

-- name: GetLogDebugLease :one
SELECT enabled_until FROM log_debug_lease WHERE singleton = true;

-- name: SetLogDebugLease :one
UPDATE log_debug_lease SET enabled_until = $1, updated_at = $2
WHERE singleton = true
RETURNING enabled_until;
