-- +goose Up
CREATE TABLE sync_runs (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  phase text NOT NULL CHECK (phase IN ('recent', 'historical', 'incremental', 'reconcile')),
  state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'completed', 'cancelled')),
  checkpoint jsonb NOT NULL DEFAULT '{}',
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  window_start timestamptz,
  applied_count bigint NOT NULL DEFAULT 0 CHECK (applied_count >= 0),
  cancel_requested boolean NOT NULL DEFAULT false,
  scheduled_for timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz,
  last_success_at timestamptz,
  completed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (id, account_id),
  CHECK (octet_length(checkpoint::text) <= 65536),
  CHECK ((phase = 'recent') = (window_start IS NOT NULL)),
  CHECK ((state IN ('completed', 'cancelled')) = (completed_at IS NOT NULL)),
  CHECK (state <> 'cancelled' OR cancel_requested)
);

CREATE UNIQUE INDEX sync_runs_account_phase_active_unique
  ON sync_runs (account_id, phase) WHERE state IN ('queued', 'running');
CREATE INDEX sync_runs_due_idx
  ON sync_runs (scheduled_for, id) WHERE state = 'queued' AND NOT cancel_requested;

-- +goose Down
DROP TABLE IF EXISTS sync_runs;
