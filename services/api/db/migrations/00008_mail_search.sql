-- +goose Up
DROP INDEX IF EXISTS messages_search_idx;
ALTER TABLE messages DROP COLUMN search_vector;
ALTER TABLE messages ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
  setweight(to_tsvector('simple', coalesce(subject, '')), 'A') ||
  setweight(to_tsvector('simple', coalesce(body_text, '')), 'B')
) STORED;
CREATE INDEX messages_search_idx ON messages USING gin(search_vector);

CREATE INDEX message_addresses_address_trgm_idx
  ON message_addresses USING gin (lower(address) gin_trgm_ops);

ALTER TABLE mailboxes ADD CONSTRAINT mailboxes_id_account_unique UNIQUE (id, account_id);
ALTER TABLE labels ADD CONSTRAINT labels_id_account_unique UNIQUE (id, account_id);

CREATE TABLE message_mailboxes (
  message_id uuid NOT NULL,
  mailbox_id uuid NOT NULL,
  account_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (message_id, mailbox_id),
  CONSTRAINT message_mailboxes_message_account_fkey
    FOREIGN KEY (message_id, account_id) REFERENCES messages(id, account_id) ON DELETE CASCADE,
  CONSTRAINT message_mailboxes_mailbox_account_fkey
    FOREIGN KEY (mailbox_id, account_id) REFERENCES mailboxes(id, account_id) ON DELETE CASCADE
);

CREATE INDEX message_mailboxes_account_mailbox_idx
  ON message_mailboxes (account_id, mailbox_id, message_id);

CREATE TABLE message_labels (
  message_id uuid NOT NULL,
  label_id uuid NOT NULL,
  account_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (message_id, label_id),
  CONSTRAINT message_labels_message_account_fkey
    FOREIGN KEY (message_id, account_id) REFERENCES messages(id, account_id) ON DELETE CASCADE,
  CONSTRAINT message_labels_label_account_fkey
    FOREIGN KEY (label_id, account_id) REFERENCES labels(id, account_id) ON DELETE CASCADE
);

CREATE INDEX message_labels_account_label_idx
  ON message_labels (account_id, label_id, message_id);

-- +goose Down
DROP TABLE IF EXISTS message_labels;
DROP TABLE IF EXISTS message_mailboxes;
ALTER TABLE labels DROP CONSTRAINT IF EXISTS labels_id_account_unique;
ALTER TABLE mailboxes DROP CONSTRAINT IF EXISTS mailboxes_id_account_unique;
DROP INDEX IF EXISTS message_addresses_address_trgm_idx;

DROP INDEX IF EXISTS messages_search_idx;
ALTER TABLE messages DROP COLUMN search_vector;
ALTER TABLE messages ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
  to_tsvector('simple', coalesce(subject, '') || ' ' || coalesce(body_text, ''))
) STORED;
CREATE INDEX messages_search_idx ON messages USING gin(search_vector);
