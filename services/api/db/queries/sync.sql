-- name: CreateSyncCursor :one
INSERT INTO sync_cursors (
  account_id, kind, cursor, state, checkpoint, uid_validity,
  version, last_success_at
)
SELECT
  accounts.id, sqlc.arg(kind), sqlc.arg(cursor), 'active', 0,
  sqlc.narg(uid_validity), 1, now()
FROM accounts
WHERE accounts.id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.disabled_at IS NULL
ON CONFLICT (account_id) DO NOTHING
RETURNING *;

-- name: GetSyncCursorByOwner :one
SELECT sync_cursors.*
FROM sync_cursors
JOIN accounts ON accounts.id = sync_cursors.account_id
WHERE sync_cursors.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id);

-- name: LockSyncCursorByOwner :one
SELECT sync_cursors.*
FROM sync_cursors
JOIN accounts ON accounts.id = sync_cursors.account_id
WHERE sync_cursors.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
FOR UPDATE OF sync_cursors;

-- name: AdvanceSyncCursor :one
UPDATE sync_cursors
SET
  cursor = sqlc.arg(cursor),
  checkpoint = checkpoint + 1,
  uid_validity = sqlc.narg(uid_validity),
  version = version + 1,
  last_success_at = now(),
  updated_at = now()
WHERE account_id = sqlc.arg(account_id)
  AND version = sqlc.arg(expected_version)
  AND state = 'active'
RETURNING *;

-- name: InvalidateSyncCursor :one
UPDATE sync_cursors
SET
  state = 'resync_required',
  invalidated_at = now(),
  invalidation_reason = sqlc.arg(reason),
  version = version + 1,
  updated_at = now()
WHERE account_id = sqlc.arg(account_id)
RETURNING *;
