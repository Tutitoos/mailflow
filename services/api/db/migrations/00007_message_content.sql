-- +goose Up
ALTER TABLE messages
  ADD COLUMN in_reply_to text[] NOT NULL DEFAULT '{}',
  ADD COLUMN content_updated_at timestamptz,
  ADD CONSTRAINT messages_subject_size CHECK (octet_length(subject) <= 1048576),
  ADD CONSTRAINT messages_body_text_size CHECK (octet_length(body_text) <= 10485760),
  ADD CONSTRAINT messages_body_html_size CHECK (octet_length(body_html_sanitized) <= 10485760);

CREATE TABLE message_attachments (
  id uuid PRIMARY KEY,
  message_id uuid NOT NULL,
  account_id uuid NOT NULL,
  position integer NOT NULL CHECK (position >= 0),
  remote_id text,
  filename text,
  media_type text NOT NULL,
  disposition text NOT NULL CHECK (disposition IN ('attachment', 'inline')),
  content_id text,
  size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT message_attachments_message_account_fkey
    FOREIGN KEY (message_id, account_id) REFERENCES messages(id, account_id) ON DELETE CASCADE,
  CONSTRAINT message_attachments_message_position_unique UNIQUE (message_id, position),
  CONSTRAINT message_attachments_remote_id_not_blank CHECK (remote_id IS NULL OR btrim(remote_id) <> ''),
  CONSTRAINT message_attachments_filename_not_blank CHECK (filename IS NULL OR btrim(filename) <> ''),
  CONSTRAINT message_attachments_media_type_not_blank CHECK (btrim(media_type) <> ''),
  CONSTRAINT message_attachments_content_id_not_blank CHECK (content_id IS NULL OR btrim(content_id) <> '')
);

CREATE INDEX message_attachments_account_message_idx
  ON message_attachments (account_id, message_id, position);

-- +goose Down
DROP TABLE IF EXISTS message_attachments;

ALTER TABLE messages
  DROP CONSTRAINT IF EXISTS messages_body_html_size,
  DROP CONSTRAINT IF EXISTS messages_body_text_size,
  DROP CONSTRAINT IF EXISTS messages_subject_size,
  DROP COLUMN IF EXISTS content_updated_at,
  DROP COLUMN IF EXISTS in_reply_to;
