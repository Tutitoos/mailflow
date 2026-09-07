-- name: RecentLogs :many
SELECT id, occurred_at, service, module, level, event, request_id, attributes
FROM log_entries
WHERE occurred_at >= sqlc.arg(since)
ORDER BY occurred_at DESC
LIMIT sqlc.arg(result_limit);
