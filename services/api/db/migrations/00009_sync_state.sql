-- +goose Up
UPDATE sync_cursors
SET kind = CASE kind
  WHEN 'gmail' THEN 'google_history'
  WHEN 'microsoft' THEN 'microsoft_delta'
  WHEN 'imap' THEN 'imap_uid'
  ELSE kind
END;

ALTER TABLE sync_cursors
  ADD COLUMN state text NOT NULL DEFAULT 'active',
  ADD COLUMN checkpoint bigint NOT NULL DEFAULT 0,
  ADD COLUMN uid_validity bigint,
  ADD COLUMN version bigint NOT NULL DEFAULT 1,
  ADD COLUMN last_success_at timestamptz,
  ADD COLUMN invalidated_at timestamptz,
  ADD COLUMN invalidation_reason text,
  ADD CONSTRAINT sync_cursors_kind_check CHECK (kind IN ('google_history', 'microsoft_delta', 'imap_uid')),
  ADD CONSTRAINT sync_cursors_state_check CHECK (state IN ('active', 'resync_required')),
  ADD CONSTRAINT sync_cursors_checkpoint_nonnegative CHECK (checkpoint >= 0),
  ADD CONSTRAINT sync_cursors_version_positive CHECK (version > 0),
  ADD CONSTRAINT sync_cursors_uid_validity_positive CHECK (uid_validity IS NULL OR uid_validity > 0),
  ADD CONSTRAINT sync_cursors_cursor_size CHECK (octet_length(cursor::text) <= 65536),
  ADD CONSTRAINT sync_cursors_shape CHECK (
    (kind = 'imap_uid' AND uid_validity IS NOT NULL)
    OR (kind <> 'imap_uid' AND uid_validity IS NULL)
  ),
  ADD CONSTRAINT sync_cursors_invalidation_shape CHECK (
    (state = 'active' AND invalidated_at IS NULL AND invalidation_reason IS NULL)
    OR (state = 'resync_required' AND invalidated_at IS NOT NULL AND invalidation_reason IS NOT NULL)
  );

-- +goose Down
ALTER TABLE sync_cursors
  DROP CONSTRAINT IF EXISTS sync_cursors_invalidation_shape,
  DROP CONSTRAINT IF EXISTS sync_cursors_shape,
  DROP CONSTRAINT IF EXISTS sync_cursors_cursor_size,
  DROP CONSTRAINT IF EXISTS sync_cursors_uid_validity_positive,
  DROP CONSTRAINT IF EXISTS sync_cursors_version_positive,
  DROP CONSTRAINT IF EXISTS sync_cursors_checkpoint_nonnegative,
  DROP CONSTRAINT IF EXISTS sync_cursors_state_check,
  DROP CONSTRAINT IF EXISTS sync_cursors_kind_check,
  DROP COLUMN IF EXISTS invalidation_reason,
  DROP COLUMN IF EXISTS invalidated_at,
  DROP COLUMN IF EXISTS last_success_at,
  DROP COLUMN IF EXISTS version,
  DROP COLUMN IF EXISTS uid_validity,
  DROP COLUMN IF EXISTS checkpoint,
  DROP COLUMN IF EXISTS state;

UPDATE sync_cursors
SET kind = CASE kind
  WHEN 'google_history' THEN 'gmail'
  WHEN 'microsoft_delta' THEN 'microsoft'
  WHEN 'imap_uid' THEN 'imap'
  ELSE kind
END;
