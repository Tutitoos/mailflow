-- +goose Up
ALTER TABLE mailboxes RENAME COLUMN name TO remote_name;

ALTER TABLE mailboxes
  ADD COLUMN local_name text,
  ADD COLUMN selectable boolean NOT NULL DEFAULT true,
  ADD COLUMN total_count integer NOT NULL DEFAULT 0,
  ADD COLUMN remote_revision text,
  ADD COLUMN last_synced_at timestamptz,
  ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

UPDATE mailboxes
SET role = NULL
WHERE role IS NOT NULL
  AND role NOT IN ('inbox', 'sent', 'drafts', 'trash', 'junk', 'archive', 'all');

UPDATE mailboxes
SET total_count = unread_count
WHERE total_count < unread_count;

ALTER TABLE mailboxes
  ADD CONSTRAINT mailboxes_remote_id_not_blank CHECK (btrim(remote_id) <> ''),
  ADD CONSTRAINT mailboxes_remote_name_not_blank CHECK (btrim(remote_name) <> ''),
  ADD CONSTRAINT mailboxes_local_name_not_blank CHECK (local_name IS NULL OR btrim(local_name) <> ''),
  ADD CONSTRAINT mailboxes_role_check CHECK (
    role IS NULL OR role IN ('inbox', 'sent', 'drafts', 'trash', 'junk', 'archive', 'all')
  ),
  ADD CONSTRAINT mailboxes_counts_nonnegative CHECK (total_count >= 0 AND unread_count >= 0),
  ADD CONSTRAINT mailboxes_unread_within_total CHECK (unread_count <= total_count);

CREATE INDEX mailboxes_account_role_name_idx
  ON mailboxes (account_id, role, remote_name, id);

CREATE TABLE labels (
  id uuid PRIMARY KEY,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  remote_id text,
  remote_name text NOT NULL,
  local_name text,
  kind text NOT NULL CHECK (kind IN ('system', 'user', 'category')),
  category text CHECK (category IN ('primary', 'promotions', 'social', 'notifications', 'forums')),
  color text,
  total_count integer NOT NULL DEFAULT 0,
  unread_count integer NOT NULL DEFAULT 0,
  remote_revision text,
  last_synced_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT labels_remote_id_not_blank CHECK (remote_id IS NULL OR btrim(remote_id) <> ''),
  CONSTRAINT labels_remote_name_not_blank CHECK (btrim(remote_name) <> ''),
  CONSTRAINT labels_local_name_not_blank CHECK (local_name IS NULL OR btrim(local_name) <> ''),
  CONSTRAINT labels_color_not_blank CHECK (color IS NULL OR btrim(color) <> ''),
  CONSTRAINT labels_counts_nonnegative CHECK (total_count >= 0 AND unread_count >= 0),
  CONSTRAINT labels_unread_within_total CHECK (unread_count <= total_count),
  CONSTRAINT labels_category_shape CHECK (
    (kind = 'category' AND category IS NOT NULL AND remote_id IS NULL)
    OR (kind <> 'category' AND category IS NULL AND remote_id IS NOT NULL)
  )
);

CREATE UNIQUE INDEX labels_account_remote_unique
  ON labels (account_id, remote_id)
  WHERE remote_id IS NOT NULL;

CREATE UNIQUE INDEX labels_account_category_unique
  ON labels (account_id, category)
  WHERE kind = 'category';

CREATE INDEX labels_account_kind_name_idx
  ON labels (account_id, kind, remote_name, id);

-- +goose Down
DROP TABLE IF EXISTS labels;
DROP INDEX IF EXISTS mailboxes_account_role_name_idx;

ALTER TABLE mailboxes
  DROP CONSTRAINT IF EXISTS mailboxes_unread_within_total,
  DROP CONSTRAINT IF EXISTS mailboxes_counts_nonnegative,
  DROP CONSTRAINT IF EXISTS mailboxes_role_check,
  DROP CONSTRAINT IF EXISTS mailboxes_local_name_not_blank,
  DROP CONSTRAINT IF EXISTS mailboxes_remote_name_not_blank,
  DROP CONSTRAINT IF EXISTS mailboxes_remote_id_not_blank,
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS created_at,
  DROP COLUMN IF EXISTS last_synced_at,
  DROP COLUMN IF EXISTS remote_revision,
  DROP COLUMN IF EXISTS total_count,
  DROP COLUMN IF EXISTS selectable,
  DROP COLUMN IF EXISTS local_name;

ALTER TABLE mailboxes RENAME COLUMN remote_name TO name;
