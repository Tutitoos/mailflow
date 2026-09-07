-- name: CreateSyncRun :one
INSERT INTO sync_runs (id, account_id, phase, checkpoint, window_start, scheduled_for)
SELECT sqlc.arg(id), accounts.id, sqlc.arg(phase), sqlc.arg(checkpoint), sqlc.narg(window_start), sqlc.arg(scheduled_for)
FROM accounts
WHERE accounts.id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.disabled_at IS NULL
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetSyncRunByOwner :one
SELECT sync_runs.* FROM sync_runs
JOIN accounts ON accounts.id = sync_runs.account_id
WHERE sync_runs.id = sqlc.arg(id)
  AND sync_runs.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id);

-- name: LockSyncRunByOwner :one
SELECT sync_runs.* FROM sync_runs
JOIN accounts ON accounts.id = sync_runs.account_id
WHERE sync_runs.id = sqlc.arg(id)
  AND sync_runs.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
FOR UPDATE OF sync_runs;

-- name: StartSyncRun :one
UPDATE sync_runs SET
  state = 'running',
  started_at = COALESCE(started_at, sqlc.arg(started_at)),
  updated_at = sqlc.arg(started_at)
WHERE id = sqlc.arg(id)
  AND account_id = sqlc.arg(account_id)
  AND version = sqlc.arg(expected_version)
  AND state = 'queued'
  AND NOT cancel_requested
RETURNING *;

-- name: CommitSyncRunPage :one
UPDATE sync_runs SET
  state = sqlc.arg(next_state),
  checkpoint = sqlc.arg(checkpoint),
  applied_count = applied_count + sqlc.arg(applied_count),
  version = version + 1,
  last_success_at = sqlc.arg(completed_at)::timestamptz,
  completed_at = CASE WHEN sqlc.arg(next_state)::text = 'completed' THEN sqlc.arg(completed_at)::timestamptz ELSE NULL::timestamptz END,
  updated_at = sqlc.arg(completed_at)::timestamptz
WHERE id = sqlc.arg(id)
  AND account_id = sqlc.arg(account_id)
  AND version = sqlc.arg(expected_version)
  AND state = 'running'
  AND NOT cancel_requested
RETURNING *;

-- name: RequestSyncRunCancellation :one
UPDATE sync_runs SET
  cancel_requested = true,
  state = 'cancelled',
  version = version + 1,
  completed_at = sqlc.arg(cancelled_at),
  updated_at = sqlc.arg(cancelled_at)
FROM accounts
WHERE sync_runs.id = sqlc.arg(id)
  AND sync_runs.account_id = sqlc.arg(account_id)
  AND sync_runs.account_id = accounts.id
  AND accounts.user_id = sqlc.arg(user_id)
  AND sync_runs.state IN ('queued', 'running')
RETURNING sync_runs.*;

-- name: ListDueSyncRuns :many
SELECT accounts.user_id, sync_runs.* FROM sync_runs
JOIN accounts ON accounts.id = sync_runs.account_id
WHERE sync_runs.state = 'queued'
  AND NOT sync_runs.cancel_requested
  AND sync_runs.scheduled_for <= sqlc.arg(due_at)
ORDER BY CASE sync_runs.phase WHEN 'recent' THEN 0 WHEN 'reconcile' THEN 1 WHEN 'incremental' THEN 2 ELSE 3 END,
         sync_runs.scheduled_for, sync_runs.id
LIMIT sqlc.arg(batch_size);

-- name: RequeueSyncRun :one
UPDATE sync_runs SET state = 'queued', updated_at = sqlc.arg(requeued_at)
WHERE id = sqlc.arg(id)
  AND account_id = sqlc.arg(account_id)
  AND version = sqlc.arg(expected_version)
  AND state = 'running'
  AND NOT cancel_requested
RETURNING *;

-- name: ExpediteSyncReconciliation :one
UPDATE sync_runs
SET scheduled_for = CASE WHEN state = 'queued' THEN sqlc.arg(requested_at) ELSE scheduled_for END,
    updated_at = sqlc.arg(requested_at)
FROM accounts
WHERE sync_runs.account_id = sqlc.arg(account_id)
  AND sync_runs.phase = 'reconcile'
  AND sync_runs.state IN ('queued', 'running')
  AND accounts.id = sync_runs.account_id
  AND accounts.user_id = sqlc.arg(user_id)
RETURNING sync_runs.*;
