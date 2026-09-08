-- +goose Up
CREATE TABLE admin_operations (
  id uuid PRIMARY KEY,
  actor_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  action text NOT NULL CHECK (action IN ('queue.retry_sync')),
  target_hash text NOT NULL CHECK (target_hash ~ '^[0-9a-f]{64}$'),
  idempotency_key_hash text NOT NULL CHECK (idempotency_key_hash ~ '^[0-9a-f]{64}$'),
  result text NOT NULL CHECK (result IN ('requested', 'queued', 'already_running', 'failed')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (actor_user_id, action, idempotency_key_hash)
);

CREATE INDEX admin_operations_owner_created_idx
  ON admin_operations (actor_user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS admin_operations;
