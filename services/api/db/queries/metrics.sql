-- name: UpsertMetricMinute :execrows
INSERT INTO metric_points (
  bucket, resolution, name, kind, dimensions, value, count,
  min_value, max_value, histogram, updated_at
)
VALUES ($1, 'minute', $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (bucket, resolution, name, dimensions) DO UPDATE SET
  value = CASE
    WHEN EXCLUDED.kind = 'gauge' THEN EXCLUDED.value
    ELSE metric_points.value + EXCLUDED.value
  END,
  count = CASE
    WHEN EXCLUDED.kind = 'gauge' THEN 1
    ELSE metric_points.count + EXCLUDED.count
  END,
  min_value = CASE
    WHEN EXCLUDED.kind = 'gauge' THEN EXCLUDED.min_value
    ELSE LEAST(metric_points.min_value, EXCLUDED.min_value)
  END,
  max_value = CASE
    WHEN EXCLUDED.kind = 'gauge' THEN EXCLUDED.max_value
    ELSE GREATEST(metric_points.max_value, EXCLUDED.max_value)
  END,
  histogram = CASE
    WHEN EXCLUDED.kind = 'histogram' THEN ARRAY(
      SELECT metric_points.histogram[index] + EXCLUDED.histogram[index]
      FROM generate_subscripts(EXCLUDED.histogram, 1) AS indexes(index)
    )
    ELSE EXCLUDED.histogram
  END,
  updated_at = EXCLUDED.updated_at
WHERE metric_points.kind = EXCLUDED.kind;

-- name: ReplaceMetricPoint :exec
INSERT INTO metric_points (
  bucket, resolution, name, kind, dimensions, value, count,
  min_value, max_value, histogram, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (bucket, resolution, name, dimensions) DO UPDATE SET
  kind = EXCLUDED.kind,
  value = EXCLUDED.value,
  count = EXCLUDED.count,
  min_value = EXCLUDED.min_value,
  max_value = EXCLUDED.max_value,
  histogram = EXCLUDED.histogram,
  updated_at = EXCLUDED.updated_at;

-- name: ListMetricPoints :many
SELECT bucket, resolution, name, kind, dimensions, value, count,
       min_value, max_value, histogram, updated_at
FROM metric_points
WHERE resolution = $1
  AND bucket >= $2
  AND bucket < $3
  AND ($4::text = '' OR name = $4)
ORDER BY bucket ASC, name ASC, dimensions ASC
LIMIT $5;

-- name: ListMetricPointsForRollup :many
SELECT bucket, resolution, name, kind, dimensions, value, count,
       min_value, max_value, histogram, updated_at
FROM metric_points
WHERE resolution = $1 AND bucket >= $2 AND bucket < $3
ORDER BY bucket ASC, name ASC, dimensions ASC;

-- name: DeleteExpiredMetricPoints :execrows
DELETE FROM metric_points
WHERE (resolution = 'minute' AND bucket < $1)
   OR (resolution = 'hour' AND bucket < $2);
