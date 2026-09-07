-- name: CreatePendingAction :one
INSERT INTO pending_actions (
  id, account_id, idempotency_key, kind, target_kind, target_id,
  desired_state, authoritative_state, status, max_attempts
)
SELECT
  sqlc.arg(id), accounts.id, sqlc.arg(idempotency_key), sqlc.arg(kind),
  sqlc.arg(target_kind), sqlc.arg(target_id), sqlc.arg(desired_state), sqlc.arg(authoritative_state),
  'pending', sqlc.arg(max_attempts)
FROM accounts
WHERE accounts.id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.disabled_at IS NULL
ON CONFLICT (account_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetPendingActionByIdempotency :one
SELECT pending_actions.*
FROM pending_actions
JOIN accounts ON accounts.id = pending_actions.account_id
WHERE pending_actions.account_id = sqlc.arg(account_id)
  AND pending_actions.idempotency_key = sqlc.arg(idempotency_key)
  AND accounts.user_id = sqlc.arg(user_id);

-- name: ClaimPendingAction :one
WITH candidate AS (
  SELECT pending_actions.id
  FROM pending_actions
  JOIN accounts ON accounts.id = pending_actions.account_id
  WHERE accounts.user_id = sqlc.arg(user_id)
    AND accounts.disabled_at IS NULL
    AND pending_actions.status IN ('pending', 'retry_wait')
    AND pending_actions.available_at <= now()
  ORDER BY pending_actions.available_at, pending_actions.created_at
  FOR UPDATE OF pending_actions SKIP LOCKED
  LIMIT 1
)
UPDATE pending_actions
SET status = 'processing', attempts = attempts + 1,
    claim_token = sqlc.arg(claim_token), claimed_at = now(), updated_at = now()
FROM candidate
WHERE pending_actions.id = candidate.id
RETURNING pending_actions.*;

-- name: GetClaimedPendingAction :one
SELECT pending_actions.*
FROM pending_actions
JOIN accounts ON accounts.id = pending_actions.account_id
WHERE pending_actions.id = sqlc.arg(id)
  AND pending_actions.claim_token = sqlc.arg(claim_token)
  AND pending_actions.status = 'processing'
  AND accounts.user_id = sqlc.arg(user_id)
FOR UPDATE OF pending_actions;

-- name: CreatePendingActionAttempt :exec
INSERT INTO pending_action_attempts (action_id, attempt, claim_token)
VALUES (sqlc.arg(action_id), sqlc.arg(attempt), sqlc.arg(claim_token));

-- name: CompletePendingAction :one
UPDATE pending_actions
SET status = 'completed', claim_token = NULL, claimed_at = NULL,
    completed_at = now(), last_error_code = NULL, updated_at = now()
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token) AND status = 'processing'
RETURNING *;

-- name: RetryPendingAction :one
UPDATE pending_actions
SET status = 'retry_wait', claim_token = NULL, claimed_at = NULL,
    available_at = sqlc.arg(available_at), last_error_code = sqlc.arg(error_code), updated_at = now()
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token) AND status = 'processing'
RETURNING *;

-- name: ConflictPendingAction :one
UPDATE pending_actions
SET status = 'conflict', claim_token = NULL, claimed_at = NULL,
    authoritative_state = sqlc.arg(authoritative_state), last_error_code = sqlc.arg(error_code),
    failed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(claim_token) AND status = 'processing'
RETURNING *;

-- name: FinishPendingActionAttempt :exec
UPDATE pending_action_attempts
SET status = sqlc.arg(status), error_code = sqlc.narg(error_code), finished_at = now()
WHERE action_id = sqlc.arg(action_id)
  AND attempt = sqlc.arg(attempt)
  AND claim_token = sqlc.arg(claim_token)
  AND status = 'processing';
