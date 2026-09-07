-- +goose Up
ALTER TABLE metric_points
  ADD COLUMN min_value double precision NOT NULL DEFAULT 0,
  ADD COLUMN max_value double precision NOT NULL DEFAULT 0,
  ADD COLUMN histogram bigint[] NOT NULL DEFAULT array_fill(0::bigint, ARRAY[13]),
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

UPDATE metric_points SET min_value = value, max_value = value;

ALTER TABLE metric_points
  ADD CONSTRAINT metric_points_histogram_size CHECK (cardinality(histogram) = 13),
  ADD CONSTRAINT metric_points_dimensions_object CHECK (jsonb_typeof(dimensions) = 'object');

CREATE INDEX metric_points_query_idx ON metric_points (resolution, bucket DESC, name);

-- +goose Down
DROP INDEX IF EXISTS metric_points_query_idx;
ALTER TABLE metric_points
  DROP CONSTRAINT IF EXISTS metric_points_dimensions_object,
  DROP CONSTRAINT IF EXISTS metric_points_histogram_size,
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS histogram,
  DROP COLUMN IF EXISTS max_value,
  DROP COLUMN IF EXISTS min_value;
