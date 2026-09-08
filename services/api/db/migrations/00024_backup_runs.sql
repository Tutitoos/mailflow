-- +goose Up
CREATE TABLE backup_runtime (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  enabled boolean NOT NULL,
  repository_kind text NOT NULL CHECK (repository_kind IN ('local', 's3')),
  schedule text NOT NULL CHECK (schedule ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  timezone text NOT NULL CHECK (length(timezone) BETWEEN 1 AND 64),
  next_run_at timestamptz NOT NULL,
  heartbeat_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE backup_runs (
  id uuid PRIMARY KEY,
  trigger text NOT NULL CHECK (trigger IN ('scheduled', 'command')),
  repository_kind text NOT NULL CHECK (repository_kind IN ('local', 's3')),
  state text NOT NULL CHECK (state IN ('running', 'succeeded', 'failed')),
  snapshot_id text CHECK (snapshot_id ~ '^[0-9a-f]{8,64}$'),
  error_code text CHECK (error_code IN ('interrupted', 'dump_failed', 'staging_failed', 'repository_failed', 'snapshot_failed', 'verification_failed', 'retention_failed')),
  file_count bigint NOT NULL DEFAULT 0 CHECK (file_count >= 0),
  byte_count bigint NOT NULL DEFAULT 0 CHECK (byte_count >= 0),
  scheduled_for timestamptz NOT NULL,
  started_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  CHECK ((state = 'running') = (completed_at IS NULL)),
  CHECK ((state = 'succeeded') = (snapshot_id IS NOT NULL)),
  CHECK ((state = 'failed') = (error_code IS NOT NULL))
);

CREATE UNIQUE INDEX backup_runs_one_active_idx ON backup_runs ((true)) WHERE state = 'running';
CREATE INDEX backup_runs_started_idx ON backup_runs (started_at DESC, id DESC);

-- +goose Down
DROP TABLE IF EXISTS backup_runs;
DROP TABLE IF EXISTS backup_runtime;
