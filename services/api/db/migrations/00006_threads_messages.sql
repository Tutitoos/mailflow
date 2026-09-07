-- +goose Up
UPDATE threads
SET category = 'primary'
WHERE category NOT IN ('primary', 'promotions', 'social', 'notifications', 'forums');

ALTER TABLE threads
  ADD COLUMN is_important boolean NOT NULL DEFAULT false,
  ADD COLUMN deleted_at timestamptz,
  ADD COLUMN message_count integer NOT NULL DEFAULT 0,
  ADD COLUMN unread_count integer NOT NULL DEFAULT 0,
  ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
  ADD CONSTRAINT threads_category_check CHECK (
    category IN ('primary', 'promotions', 'social', 'notifications', 'forums')
  ),
  ADD CONSTRAINT threads_counts_nonnegative CHECK (message_count >= 0 AND unread_count >= 0),
  ADD CONSTRAINT threads_unread_within_messages CHECK (unread_count <= message_count),
  ADD CONSTRAINT threads_id_account_unique UNIQUE (id, account_id);

DROP INDEX IF EXISTS threads_account_last_message_idx;
CREATE INDEX threads_account_last_message_idx
  ON threads (account_id, last_message_at DESC, id DESC);

ALTER TABLE messages ADD COLUMN account_id uuid;

UPDATE messages
SET account_id = threads.account_id
FROM threads
WHERE threads.id = messages.thread_id;

ALTER TABLE messages
  ALTER COLUMN account_id SET NOT NULL,
  ADD COLUMN is_read boolean NOT NULL DEFAULT false,
  ADD COLUMN is_starred boolean NOT NULL DEFAULT false,
  ADD COLUMN is_important boolean NOT NULL DEFAULT false,
  ADD COLUMN deleted_at timestamptz,
  ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
  DROP CONSTRAINT messages_thread_id_remote_id_key,
  DROP CONSTRAINT messages_thread_id_fkey,
  ADD CONSTRAINT messages_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
  ADD CONSTRAINT messages_thread_account_fkey FOREIGN KEY (thread_id, account_id) REFERENCES threads(id, account_id) ON DELETE CASCADE,
  ADD CONSTRAINT messages_account_remote_unique UNIQUE (account_id, remote_id),
  ADD CONSTRAINT messages_id_account_unique UNIQUE (id, account_id),
  ADD CONSTRAINT messages_remote_id_not_blank CHECK (btrim(remote_id) <> '');

CREATE INDEX messages_thread_sent_idx
  ON messages (thread_id, sent_at, id);

CREATE TABLE message_addresses (
  message_id uuid NOT NULL,
  account_id uuid NOT NULL,
  role text NOT NULL CHECK (role IN ('from', 'sender', 'reply_to', 'to', 'cc', 'bcc')),
  position integer NOT NULL CHECK (position >= 0),
  display_name text,
  address text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (message_id, role, position),
  CONSTRAINT message_addresses_message_account_fkey
    FOREIGN KEY (message_id, account_id) REFERENCES messages(id, account_id) ON DELETE CASCADE,
  CONSTRAINT message_addresses_name_not_blank CHECK (display_name IS NULL OR btrim(display_name) <> ''),
  CONSTRAINT message_addresses_address_not_blank CHECK (btrim(address) <> '')
);

CREATE INDEX message_addresses_account_address_idx
  ON message_addresses (account_id, lower(address), message_id);

UPDATE messages
SET
  is_read = threads.is_read,
  is_starred = threads.is_starred
FROM threads
WHERE threads.id = messages.thread_id;

UPDATE threads
SET
  message_count = summary.message_count,
  unread_count = summary.unread_count,
  is_read = summary.unread_count = 0,
  is_starred = summary.is_starred,
  is_important = summary.is_important,
  last_message_at = summary.last_message_at
FROM (
  SELECT
    thread_id,
    count(*)::integer AS message_count,
    count(*) FILTER (WHERE NOT is_read)::integer AS unread_count,
    bool_or(is_starred) AS is_starred,
    bool_or(is_important) AS is_important,
    max(sent_at) AS last_message_at
  FROM messages
  GROUP BY thread_id
) AS summary
WHERE threads.id = summary.thread_id;

-- +goose Down
DROP TABLE IF EXISTS message_addresses;
DROP INDEX IF EXISTS messages_thread_sent_idx;

ALTER TABLE messages
  DROP CONSTRAINT IF EXISTS messages_remote_id_not_blank,
  DROP CONSTRAINT IF EXISTS messages_id_account_unique,
  DROP CONSTRAINT IF EXISTS messages_account_remote_unique,
  DROP CONSTRAINT IF EXISTS messages_thread_account_fkey,
  DROP CONSTRAINT IF EXISTS messages_account_id_fkey,
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS created_at,
  DROP COLUMN IF EXISTS deleted_at,
  DROP COLUMN IF EXISTS is_important,
  DROP COLUMN IF EXISTS is_starred,
  DROP COLUMN IF EXISTS is_read,
  DROP COLUMN IF EXISTS account_id,
  ADD CONSTRAINT messages_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES threads(id) ON DELETE CASCADE,
  ADD CONSTRAINT messages_thread_id_remote_id_key UNIQUE (thread_id, remote_id);

DROP INDEX IF EXISTS threads_account_last_message_idx;

ALTER TABLE threads
  DROP CONSTRAINT IF EXISTS threads_id_account_unique,
  DROP CONSTRAINT IF EXISTS threads_unread_within_messages,
  DROP CONSTRAINT IF EXISTS threads_counts_nonnegative,
  DROP CONSTRAINT IF EXISTS threads_category_check,
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS created_at,
  DROP COLUMN IF EXISTS unread_count,
  DROP COLUMN IF EXISTS message_count,
  DROP COLUMN IF EXISTS deleted_at,
  DROP COLUMN IF EXISTS is_important;

CREATE INDEX threads_account_last_message_idx
  ON threads (account_id, last_message_at DESC);
