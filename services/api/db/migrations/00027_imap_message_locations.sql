-- +goose Up
ALTER TABLE imap_folder_cursors
  ADD COLUMN wire_name text;

UPDATE imap_folder_cursors
SET wire_name = mailboxes.remote_name
FROM mailboxes
WHERE mailboxes.id = imap_folder_cursors.mailbox_id
  AND mailboxes.account_id = imap_folder_cursors.account_id;

ALTER TABLE imap_folder_cursors
  ALTER COLUMN wire_name SET NOT NULL,
  ADD CONSTRAINT imap_folder_cursors_wire_name_not_blank CHECK (btrim(wire_name) <> '');

CREATE TABLE imap_message_locations (
  message_id uuid NOT NULL,
  mailbox_id uuid NOT NULL,
  account_id uuid NOT NULL,
  uid_validity bigint NOT NULL CHECK (uid_validity > 0),
  uid bigint NOT NULL CHECK (uid > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (message_id, mailbox_id),
  CONSTRAINT imap_message_locations_message_account_fkey
    FOREIGN KEY (message_id, account_id) REFERENCES messages(id, account_id) ON DELETE CASCADE,
  CONSTRAINT imap_message_locations_mailbox_account_fkey
    FOREIGN KEY (mailbox_id, account_id) REFERENCES mailboxes(id, account_id) ON DELETE CASCADE,
  CONSTRAINT imap_message_locations_account_mailbox_uid_unique
    UNIQUE (account_id, mailbox_id, uid_validity, uid)
);

CREATE INDEX imap_message_locations_message_idx
  ON imap_message_locations (account_id, message_id, mailbox_id);

-- +goose Down
DROP TABLE IF EXISTS imap_message_locations;
ALTER TABLE imap_folder_cursors
  DROP CONSTRAINT IF EXISTS imap_folder_cursors_wire_name_not_blank,
  DROP COLUMN IF EXISTS wire_name;
