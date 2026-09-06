-- name: RecentLogs :many
SELECT id, occurred_at, service, module, level, event, request_id, attributes
FROM log_entries
WHERE occurred_at >= sqlc.arg(since)
ORDER BY occurred_at DESC
LIMIT sqlc.arg(result_limit);

-- name: UpsertMetricPoint :exec
INSERT INTO metric_points (bucket, resolution, name, kind, dimensions, value, count)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (bucket, resolution, name, dimensions)
DO UPDATE SET value = EXCLUDED.value, count = metric_points.count + EXCLUDED.count;
