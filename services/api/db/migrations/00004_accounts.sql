-- +goose Up
ALTER TABLE accounts
  ADD COLUMN remote_id text,
  ADD COLUMN disabled_at timestamptz,
  ADD CONSTRAINT accounts_sync_state_check CHECK (sync_state IN ('pending', 'syncing', 'idle', 'error', 'disabled'));

UPDATE accounts SET remote_id = id::text WHERE remote_id IS NULL;
ALTER TABLE accounts ALTER COLUMN remote_id SET NOT NULL;
ALTER TABLE accounts ADD CONSTRAINT accounts_user_provider_remote_unique UNIQUE (user_id, provider, remote_id);

CREATE INDEX accounts_user_created_idx ON accounts(user_id, created_at, id);

-- +goose Down
DROP INDEX IF EXISTS accounts_user_created_idx;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_user_provider_remote_unique;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_sync_state_check;
ALTER TABLE accounts DROP COLUMN IF EXISTS disabled_at;
ALTER TABLE accounts DROP COLUMN IF EXISTS remote_id;
