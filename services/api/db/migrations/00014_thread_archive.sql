-- +goose Up
ALTER TABLE threads ADD COLUMN archived_at timestamptz;
CREATE INDEX threads_inbox_idx ON threads(account_id, category, last_message_at DESC, id DESC)
  WHERE deleted_at IS NULL AND archived_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS threads_inbox_idx;
ALTER TABLE threads DROP COLUMN IF EXISTS archived_at;
