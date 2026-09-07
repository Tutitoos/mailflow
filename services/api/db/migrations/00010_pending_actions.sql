-- +goose Up
ALTER TABLE pending_actions
  RENAME COLUMN action TO kind;
ALTER TABLE pending_actions
  RENAME COLUMN payload TO desired_state;

ALTER TABLE pending_actions
  ADD COLUMN target_kind text NOT NULL DEFAULT 'thread',
  ADD COLUMN target_id uuid,
  ADD COLUMN authoritative_state jsonb,
  ADD COLUMN max_attempts integer NOT NULL DEFAULT 5,
  ADD COLUMN claim_token uuid,
  ADD COLUMN claimed_at timestamptz,
  ADD COLUMN completed_at timestamptz,
  ADD COLUMN failed_at timestamptz,
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

UPDATE pending_actions SET target_id = id WHERE target_id IS NULL;

ALTER TABLE pending_actions
  ALTER COLUMN target_id SET NOT NULL,
  ALTER COLUMN target_kind DROP DEFAULT,
  ADD CONSTRAINT pending_actions_target_kind_check CHECK (target_kind IN ('thread', 'message')),
  ADD CONSTRAINT pending_actions_status_check CHECK (status IN ('pending', 'processing', 'retry_wait', 'completed', 'conflict')),
  ADD CONSTRAINT pending_actions_attempts_check CHECK (attempts >= 0 AND attempts <= max_attempts),
  ADD CONSTRAINT pending_actions_max_attempts_check CHECK (max_attempts BETWEEN 1 AND 20),
  ADD CONSTRAINT pending_actions_claim_check CHECK (
    (status = 'processing' AND claim_token IS NOT NULL AND claimed_at IS NOT NULL)
    OR (status <> 'processing' AND claim_token IS NULL AND claimed_at IS NULL)
  ),
  ADD CONSTRAINT pending_actions_terminal_check CHECK (
    (status = 'completed' AND completed_at IS NOT NULL AND failed_at IS NULL)
    OR (status = 'conflict' AND failed_at IS NOT NULL AND completed_at IS NULL AND authoritative_state IS NOT NULL)
    OR (status NOT IN ('completed', 'conflict') AND completed_at IS NULL AND failed_at IS NULL)
  );

CREATE INDEX pending_actions_claim_idx
  ON pending_actions(status, available_at, created_at)
  WHERE status IN ('pending', 'retry_wait');

CREATE TABLE pending_action_attempts (
  action_id uuid NOT NULL REFERENCES pending_actions(id) ON DELETE CASCADE,
  attempt integer NOT NULL,
  claim_token uuid NOT NULL,
  status text NOT NULL DEFAULT 'processing' CHECK (status IN ('processing', 'completed', 'retry', 'conflict')),
  error_code text,
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  PRIMARY KEY (action_id, attempt),
  UNIQUE (claim_token),
  CHECK (
    (status = 'processing' AND finished_at IS NULL AND error_code IS NULL)
    OR (status = 'completed' AND finished_at IS NOT NULL AND error_code IS NULL)
    OR (status IN ('retry', 'conflict') AND finished_at IS NOT NULL AND error_code IS NOT NULL)
  )
);

-- +goose Down
DROP TABLE IF EXISTS pending_action_attempts;
DROP INDEX IF EXISTS pending_actions_claim_idx;

ALTER TABLE pending_actions
  DROP CONSTRAINT IF EXISTS pending_actions_terminal_check,
  DROP CONSTRAINT IF EXISTS pending_actions_claim_check,
  DROP CONSTRAINT IF EXISTS pending_actions_max_attempts_check,
  DROP CONSTRAINT IF EXISTS pending_actions_attempts_check,
  DROP CONSTRAINT IF EXISTS pending_actions_status_check,
  DROP CONSTRAINT IF EXISTS pending_actions_target_kind_check,
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS failed_at,
  DROP COLUMN IF EXISTS completed_at,
  DROP COLUMN IF EXISTS claimed_at,
  DROP COLUMN IF EXISTS claim_token,
  DROP COLUMN IF EXISTS max_attempts,
  DROP COLUMN IF EXISTS authoritative_state,
  DROP COLUMN IF EXISTS target_id,
  DROP COLUMN IF EXISTS target_kind;

ALTER TABLE pending_actions
  RENAME COLUMN desired_state TO payload;
ALTER TABLE pending_actions
  RENAME COLUMN kind TO action;
