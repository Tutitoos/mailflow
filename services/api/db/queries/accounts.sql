-- name: CreateAccount :one
INSERT INTO accounts (
  id, user_id, provider, remote_id, display_name,
  encrypted_credentials, credential_nonce, capabilities
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, user_id, provider, remote_id, display_name, capabilities, sync_state, disabled_at, created_at, updated_at;

-- name: ListAccountsByUser :many
SELECT id, user_id, provider, remote_id, display_name, capabilities, sync_state, disabled_at, created_at, updated_at
FROM accounts
WHERE user_id = $1
ORDER BY created_at, id;

-- name: GetAccountByUser :one
SELECT id, user_id, provider, remote_id, display_name, capabilities, sync_state, disabled_at, created_at, updated_at
FROM accounts
WHERE id = $1 AND user_id = $2;

-- name: GetAccountCredentialsByUser :one
SELECT encrypted_credentials, credential_nonce
FROM accounts
WHERE id = $1 AND user_id = $2;

-- name: GetAccountByProviderRemote :one
SELECT id, user_id, provider, remote_id, display_name, capabilities, sync_state, disabled_at, created_at, updated_at
FROM accounts
WHERE user_id = sqlc.arg(user_id)
  AND provider = sqlc.arg(provider)
  AND remote_id = sqlc.arg(remote_id);

-- name: ReplaceAccountCredentials :one
UPDATE accounts
SET encrypted_credentials = sqlc.arg(encrypted_credentials),
    credential_nonce = sqlc.arg(credential_nonce),
    display_name = sqlc.arg(display_name),
    capabilities = sqlc.arg(capabilities),
    sync_state = 'pending',
    disabled_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING id, user_id, provider, remote_id, display_name, capabilities, sync_state, disabled_at, created_at, updated_at;

-- name: UpdateAccountCapabilities :one
UPDATE accounts
SET capabilities = $3, updated_at = now()
WHERE id = $1 AND user_id = $2 AND disabled_at IS NULL
RETURNING id, user_id, provider, remote_id, display_name, capabilities, sync_state, disabled_at, created_at, updated_at;

-- name: MarkAccountError :one
UPDATE accounts
SET sync_state = 'error', updated_at = now()
WHERE id = $1 AND user_id = $2 AND disabled_at IS NULL
RETURNING id, user_id, provider, remote_id, display_name, capabilities, sync_state, disabled_at, created_at, updated_at;

-- name: DisableAccount :one
UPDATE accounts
SET disabled_at = COALESCE(disabled_at, now()), sync_state = 'disabled', updated_at = now()
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, provider, remote_id, display_name, capabilities, sync_state, disabled_at, created_at, updated_at;

-- name: DisableAccountAndClearCredentials :one
UPDATE accounts
SET encrypted_credentials = sqlc.arg(encrypted_credentials),
    credential_nonce = sqlc.arg(credential_nonce),
    capabilities = '{}'::jsonb,
    disabled_at = COALESCE(disabled_at, now()),
    sync_state = 'disabled',
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND provider = 'imap'
RETURNING id, user_id, provider, remote_id, display_name, capabilities, sync_state, disabled_at, created_at, updated_at;
