-- +goose Up
ALTER TABLE drafts
  ADD COLUMN compose_mode text NOT NULL DEFAULT 'new'
    CHECK (compose_mode IN ('new', 'reply', 'forward')),
  ADD COLUMN source_message_id uuid,
  ADD CONSTRAINT drafts_source_message_account_fkey
    FOREIGN KEY (source_message_id, account_id) REFERENCES messages(id, account_id) ON DELETE SET NULL (source_message_id);

CREATE TABLE outbound_deliveries (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  draft_id uuid NOT NULL,
  idempotency_key text NOT NULL,
  payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
  status text NOT NULL DEFAULT 'prepared' CHECK (status IN ('prepared', 'sending', 'sent', 'ambiguous')),
  remote_id text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (account_id, idempotency_key),
  FOREIGN KEY (draft_id, account_id) REFERENCES drafts(id, account_id) ON DELETE RESTRICT,
  CHECK (char_length(idempotency_key) BETWEEN 16 AND 128),
  CHECK (remote_id IS NULL OR btrim(remote_id) <> '')
);

CREATE INDEX outbound_deliveries_account_created_idx
  ON outbound_deliveries (account_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS outbound_deliveries;
ALTER TABLE drafts
  DROP CONSTRAINT IF EXISTS drafts_source_message_account_fkey,
  DROP COLUMN IF EXISTS source_message_id,
  DROP COLUMN IF EXISTS compose_mode;
