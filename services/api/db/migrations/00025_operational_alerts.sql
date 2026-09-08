-- +goose Up
CREATE TABLE alert_incidents (
  id uuid PRIMARY KEY,
  deduplication_hash text NOT NULL UNIQUE CHECK (deduplication_hash ~ '^[0-9a-f]{64}$'),
  policy text NOT NULL CHECK (policy IN ('provider_auth', 'sync_backlog', 'disk', 'backup', 'sentry_ingestion', 'service_health', 'test')),
  source text NOT NULL CHECK (source ~ '^[a-z][a-z0-9_.-]{0,63}$'),
  code text NOT NULL CHECK (code ~ '^[a-z][a-z0-9_.-]{0,63}$'),
  state text NOT NULL CHECK (state IN ('active', 'recovered')),
  opened_at timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  recovered_at timestamptz,
  cooldown_until timestamptz NOT NULL,
  CHECK ((state = 'recovered') = (recovered_at IS NOT NULL))
);

CREATE TABLE alert_deliveries (
  id uuid PRIMARY KEY,
  incident_id uuid NOT NULL REFERENCES alert_incidents(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('incident', 'reminder', 'recovery', 'test')),
  channel text NOT NULL CHECK (channel IN ('smtp', 'connected_account')),
  status text NOT NULL CHECK (status IN ('pending', 'sent', 'failed', 'suppressed')),
  error_code text CHECK (error_code IN ('smtp_unavailable', 'smtp_rejected', 'fallback_disabled', 'fallback_recursion', 'fallback_failed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  CHECK ((status = 'pending') = (completed_at IS NULL))
);

CREATE INDEX alert_incidents_last_seen_idx ON alert_incidents (last_seen_at DESC, id DESC);
CREATE INDEX alert_deliveries_created_idx ON alert_deliveries (created_at DESC, id DESC);

-- +goose Down
DROP TABLE IF EXISTS alert_deliveries;
DROP TABLE IF EXISTS alert_incidents;
