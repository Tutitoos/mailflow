-- +goose Up
CREATE TABLE drafts (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  remote_id text,
  remote_revision text,
  subject text NOT NULL DEFAULT '',
  body_text text NOT NULL DEFAULT '',
  body_html_sanitized text NOT NULL DEFAULT '',
  local_revision bigint NOT NULL DEFAULT 1 CHECK (local_revision > 0),
  synced_revision bigint NOT NULL DEFAULT 0 CHECK (synced_revision >= 0 AND synced_revision <= local_revision),
  sync_status text NOT NULL DEFAULT 'queued' CHECK (sync_status IN ('queued', 'syncing', 'synced', 'conflict', 'discarded')),
  remote_checkpoint_at timestamptz NOT NULL DEFAULT (now() + interval '15 seconds'),
  last_remote_synced_at timestamptz,
  discarded_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (id, account_id),
  CHECK (octet_length(subject) <= 1048576),
  CHECK (octet_length(body_text) <= 10485760),
  CHECK (octet_length(body_html_sanitized) <= 10485760),
  CHECK (remote_id IS NULL OR btrim(remote_id) <> ''),
  CHECK (remote_revision IS NULL OR btrim(remote_revision) <> ''),
  CHECK ((sync_status = 'discarded') = (discarded_at IS NOT NULL))
);

CREATE UNIQUE INDEX drafts_account_remote_unique
  ON drafts(account_id, remote_id) WHERE remote_id IS NOT NULL;
CREATE INDEX drafts_remote_checkpoint_idx
  ON drafts(remote_checkpoint_at) WHERE sync_status = 'queued';

CREATE TABLE draft_recipients (
  draft_id uuid NOT NULL,
  account_id uuid NOT NULL,
  role text NOT NULL CHECK (role IN ('to', 'cc', 'bcc')),
  position integer NOT NULL CHECK (position >= 0),
  display_name text,
  address text NOT NULL,
  PRIMARY KEY (draft_id, role, position),
  FOREIGN KEY (draft_id, account_id) REFERENCES drafts(id, account_id) ON DELETE CASCADE,
  CHECK (btrim(address) <> ''),
  CHECK (octet_length(address) <= 1024),
  CHECK (display_name IS NULL OR octet_length(display_name) <= 256)
);

CREATE TABLE draft_attachments (
  draft_id uuid NOT NULL,
  account_id uuid NOT NULL,
  position integer NOT NULL CHECK (position >= 0),
  object_id text NOT NULL CHECK (object_id ~ '^[0-9a-f]{32}$'),
  filename text,
  media_type text NOT NULL,
  size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
  PRIMARY KEY (draft_id, position),
  UNIQUE (draft_id, object_id),
  FOREIGN KEY (draft_id, account_id) REFERENCES drafts(id, account_id) ON DELETE CASCADE,
  CHECK (filename IS NULL OR (btrim(filename) <> '' AND octet_length(filename) <= 1024)),
  CHECK (btrim(media_type) <> '' AND octet_length(media_type) <= 255)
);

-- +goose Down
DROP TABLE IF EXISTS draft_attachments;
DROP TABLE IF EXISTS draft_recipients;
DROP TABLE IF EXISTS drafts;
