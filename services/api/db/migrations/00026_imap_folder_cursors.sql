-- +goose Up
CREATE TABLE imap_folder_cursors (
  mailbox_id uuid NOT NULL,
  account_id uuid NOT NULL,
  identity_key text NOT NULL,
  namespace_prefix text NOT NULL DEFAULT '',
  delimiter text,
  subscribed boolean NOT NULL DEFAULT false,
  uid_next bigint,
  uid_validity bigint,
  next_uid bigint,
  state text NOT NULL CHECK (state IN ('active', 'resync_required', 'not_selectable', 'missing')),
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  invalidated_at timestamptz,
  invalidation_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (mailbox_id, account_id),
  CONSTRAINT imap_folder_cursors_mailbox_fkey
    FOREIGN KEY (mailbox_id, account_id) REFERENCES mailboxes(id, account_id) ON DELETE CASCADE,
  CONSTRAINT imap_folder_cursors_identity_not_blank CHECK (btrim(identity_key) <> ''),
  CONSTRAINT imap_folder_cursors_delimiter_shape CHECK (delimiter IS NULL OR char_length(delimiter) = 1),
  CONSTRAINT imap_folder_cursors_uid_next_positive CHECK (uid_next IS NULL OR uid_next > 0),
  CONSTRAINT imap_folder_cursors_uid_validity_positive CHECK (uid_validity IS NULL OR uid_validity > 0),
  CONSTRAINT imap_folder_cursors_next_uid_positive CHECK (next_uid IS NULL OR next_uid > 0),
  CONSTRAINT imap_folder_cursors_selectable_shape CHECK (
    (state IN ('active', 'resync_required') AND uid_validity IS NOT NULL AND next_uid IS NOT NULL)
    OR (state IN ('not_selectable', 'missing') AND next_uid IS NULL)
  ),
  CONSTRAINT imap_folder_cursors_invalidation_shape CHECK (
    (state = 'resync_required' AND invalidated_at IS NOT NULL AND invalidation_reason = 'uid_validity_changed')
    OR (state <> 'resync_required' AND invalidated_at IS NULL AND invalidation_reason IS NULL)
  )
);

CREATE UNIQUE INDEX imap_folder_cursors_account_identity_unique
  ON imap_folder_cursors (account_id, identity_key);

CREATE INDEX imap_folder_cursors_account_state_idx
  ON imap_folder_cursors (account_id, state, mailbox_id);

-- +goose Down
DROP TABLE IF EXISTS imap_folder_cursors;
