-- name: ReconcileMailbox :one
INSERT INTO mailboxes (
  id, account_id, remote_id, remote_name, role, selectable,
  total_count, unread_count, remote_revision, last_synced_at
)
SELECT
  sqlc.arg(id), owned.id, sqlc.arg(remote_id), sqlc.arg(remote_name),
  sqlc.narg(role), sqlc.arg(selectable), sqlc.arg(total_count),
  sqlc.arg(unread_count), sqlc.narg(remote_revision), sqlc.narg(last_synced_at)
FROM accounts AS owned
WHERE owned.id = sqlc.arg(account_id)
  AND owned.user_id = sqlc.arg(user_id)
  AND owned.disabled_at IS NULL
ON CONFLICT (account_id, remote_id) DO UPDATE SET
  remote_name = EXCLUDED.remote_name,
  role = EXCLUDED.role,
  selectable = EXCLUDED.selectable,
  total_count = EXCLUDED.total_count,
  unread_count = EXCLUDED.unread_count,
  remote_revision = EXCLUDED.remote_revision,
  last_synced_at = EXCLUDED.last_synced_at,
  updated_at = now()
RETURNING *;

-- name: ListMailboxesByAccount :many
SELECT mailboxes.*
FROM mailboxes
JOIN accounts ON accounts.id = mailboxes.account_id
WHERE mailboxes.account_id = $1
  AND accounts.user_id = $2
ORDER BY
  CASE mailboxes.role
    WHEN 'inbox' THEN 0
    WHEN 'sent' THEN 1
    WHEN 'drafts' THEN 2
    WHEN 'archive' THEN 3
    WHEN 'junk' THEN 4
    WHEN 'trash' THEN 5
    WHEN 'all' THEN 6
    ELSE 7
  END,
  COALESCE(mailboxes.local_name, mailboxes.remote_name),
  mailboxes.id;

-- name: RenameMailboxLocal :one
UPDATE mailboxes
SET local_name = NULLIF(btrim(sqlc.arg(local_name)::text), ''), updated_at = now()
FROM accounts
WHERE mailboxes.id = sqlc.arg(id)
  AND mailboxes.account_id = sqlc.arg(account_id)
  AND accounts.id = mailboxes.account_id
  AND accounts.user_id = sqlc.arg(user_id)
RETURNING mailboxes.*;

-- name: UpdateMailboxCounters :one
UPDATE mailboxes
SET total_count = sqlc.arg(total_count), unread_count = sqlc.arg(unread_count), updated_at = now()
FROM accounts
WHERE mailboxes.id = sqlc.arg(id)
  AND mailboxes.account_id = sqlc.arg(account_id)
  AND accounts.id = mailboxes.account_id
  AND accounts.user_id = sqlc.arg(user_id)
RETURNING mailboxes.*;

-- name: ReconcileProviderLabel :one
INSERT INTO labels (
  id, account_id, remote_id, remote_name, kind, color,
  total_count, unread_count, remote_revision, last_synced_at
)
SELECT
  sqlc.arg(id), owned.id, sqlc.arg(remote_id), sqlc.arg(remote_name),
  sqlc.arg(kind), sqlc.narg(color), sqlc.arg(total_count),
  sqlc.arg(unread_count), sqlc.narg(remote_revision), sqlc.narg(last_synced_at)
FROM accounts AS owned
WHERE owned.id = sqlc.arg(account_id)
  AND owned.user_id = sqlc.arg(user_id)
  AND owned.disabled_at IS NULL
ON CONFLICT (account_id, remote_id) WHERE remote_id IS NOT NULL DO UPDATE SET
  remote_name = EXCLUDED.remote_name,
  kind = EXCLUDED.kind,
  color = EXCLUDED.color,
  total_count = EXCLUDED.total_count,
  unread_count = EXCLUDED.unread_count,
  remote_revision = EXCLUDED.remote_revision,
  last_synced_at = EXCLUDED.last_synced_at,
  updated_at = now()
RETURNING *;

-- name: EnsureCategoryLabel :one
INSERT INTO labels (
  id, account_id, remote_name, kind, category, color
)
SELECT
  sqlc.arg(id), owned.id, sqlc.arg(remote_name), 'category',
  sqlc.arg(category), sqlc.narg(color)
FROM accounts AS owned
WHERE owned.id = sqlc.arg(account_id)
  AND owned.user_id = sqlc.arg(user_id)
  AND owned.disabled_at IS NULL
ON CONFLICT (account_id, category) WHERE kind = 'category' DO UPDATE SET
  remote_name = EXCLUDED.remote_name,
  color = EXCLUDED.color,
  updated_at = now()
RETURNING *;

-- name: ListLabelsByAccount :many
SELECT labels.*
FROM labels
JOIN accounts ON accounts.id = labels.account_id
WHERE labels.account_id = $1
  AND accounts.user_id = $2
ORDER BY labels.kind, COALESCE(labels.local_name, labels.remote_name), labels.id;

-- name: RenameLabelLocal :one
UPDATE labels
SET local_name = NULLIF(btrim(sqlc.arg(local_name)::text), ''), updated_at = now()
FROM accounts
WHERE labels.id = sqlc.arg(id)
  AND labels.account_id = sqlc.arg(account_id)
  AND accounts.id = labels.account_id
  AND accounts.user_id = sqlc.arg(user_id)
RETURNING labels.*;

-- name: UpdateLabelCounters :one
UPDATE labels
SET total_count = sqlc.arg(total_count), unread_count = sqlc.arg(unread_count), updated_at = now()
FROM accounts
WHERE labels.id = sqlc.arg(id)
  AND labels.account_id = sqlc.arg(account_id)
  AND accounts.id = labels.account_id
  AND accounts.user_id = sqlc.arg(user_id)
RETURNING labels.*;
