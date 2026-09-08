-- name: ListIMAPFolderStatesByOwner :many
SELECT
  mailboxes.id AS mailbox_id,
  mailboxes.remote_id,
  mailboxes.remote_name,
  mailboxes.role,
  mailboxes.selectable,
  mailboxes.total_count,
  mailboxes.unread_count,
  imap_folder_cursors.identity_key,
  imap_folder_cursors.namespace_prefix,
  imap_folder_cursors.delimiter,
  imap_folder_cursors.subscribed,
  imap_folder_cursors.uid_next,
  imap_folder_cursors.uid_validity,
  imap_folder_cursors.next_uid,
  imap_folder_cursors.state,
  imap_folder_cursors.version,
  imap_folder_cursors.invalidated_at,
  imap_folder_cursors.invalidation_reason,
  imap_folder_cursors.updated_at
FROM imap_folder_cursors
JOIN mailboxes
  ON mailboxes.id = imap_folder_cursors.mailbox_id
  AND mailboxes.account_id = imap_folder_cursors.account_id
JOIN accounts ON accounts.id = imap_folder_cursors.account_id
WHERE imap_folder_cursors.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
ORDER BY mailboxes.remote_name, mailboxes.id;

-- name: UpsertIMAPFolderState :one
INSERT INTO imap_folder_cursors (
  mailbox_id, account_id, identity_key, namespace_prefix, delimiter,
  subscribed, uid_next, uid_validity, next_uid, state
) VALUES (
  sqlc.arg(mailbox_id), sqlc.arg(account_id), sqlc.arg(identity_key),
  sqlc.arg(namespace_prefix), sqlc.narg(delimiter), sqlc.arg(subscribed),
  sqlc.narg(uid_next), sqlc.narg(uid_validity), sqlc.narg(next_uid), sqlc.arg(state)
)
ON CONFLICT (mailbox_id, account_id) DO UPDATE SET
  identity_key = EXCLUDED.identity_key,
  namespace_prefix = EXCLUDED.namespace_prefix,
  delimiter = EXCLUDED.delimiter,
  subscribed = EXCLUDED.subscribed,
  uid_next = EXCLUDED.uid_next,
  uid_validity = EXCLUDED.uid_validity,
  next_uid = CASE
    WHEN imap_folder_cursors.uid_validity IS DISTINCT FROM EXCLUDED.uid_validity
      AND imap_folder_cursors.uid_validity IS NOT NULL
      AND EXCLUDED.uid_validity IS NOT NULL THEN 1
    ELSE EXCLUDED.next_uid
  END,
  state = CASE
    WHEN EXCLUDED.state IN ('not_selectable', 'missing') THEN EXCLUDED.state
    WHEN imap_folder_cursors.uid_validity IS DISTINCT FROM EXCLUDED.uid_validity
      AND imap_folder_cursors.uid_validity IS NOT NULL THEN 'resync_required'
    WHEN imap_folder_cursors.state = 'resync_required' THEN 'resync_required'
    ELSE EXCLUDED.state
  END,
  version = imap_folder_cursors.version + 1,
  invalidated_at = CASE
    WHEN EXCLUDED.state IN ('not_selectable', 'missing') THEN NULL
    WHEN imap_folder_cursors.uid_validity IS DISTINCT FROM EXCLUDED.uid_validity
      AND imap_folder_cursors.uid_validity IS NOT NULL
      AND EXCLUDED.uid_validity IS NOT NULL THEN now()
    WHEN imap_folder_cursors.state = 'resync_required' THEN imap_folder_cursors.invalidated_at
    ELSE NULL
  END,
  invalidation_reason = CASE
    WHEN EXCLUDED.state IN ('not_selectable', 'missing') THEN NULL
    WHEN imap_folder_cursors.uid_validity IS DISTINCT FROM EXCLUDED.uid_validity
      AND imap_folder_cursors.uid_validity IS NOT NULL
      AND EXCLUDED.uid_validity IS NOT NULL THEN 'uid_validity_changed'
    WHEN imap_folder_cursors.state = 'resync_required' THEN imap_folder_cursors.invalidation_reason
    ELSE NULL
  END,
  updated_at = now()
RETURNING *;

-- name: RenameIMAPMailboxIdentity :one
UPDATE mailboxes
SET remote_id = sqlc.arg(remote_id),
    remote_name = sqlc.arg(remote_name),
    role = sqlc.narg(role),
    selectable = sqlc.arg(selectable),
    total_count = sqlc.arg(total_count),
    unread_count = sqlc.arg(unread_count),
    remote_revision = sqlc.narg(remote_revision),
    last_synced_at = now(),
    updated_at = now()
FROM accounts
WHERE mailboxes.id = sqlc.arg(mailbox_id)
  AND mailboxes.account_id = sqlc.arg(account_id)
  AND accounts.id = mailboxes.account_id
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.provider = 'imap'
  AND accounts.disabled_at IS NULL
RETURNING mailboxes.*;

-- name: MarkIMAPFolderMissing :exec
UPDATE mailboxes
SET selectable = false, updated_at = now()
FROM imap_folder_cursors, accounts
WHERE mailboxes.id = imap_folder_cursors.mailbox_id
  AND mailboxes.account_id = imap_folder_cursors.account_id
  AND accounts.id = mailboxes.account_id
  AND accounts.user_id = sqlc.arg(user_id)
  AND mailboxes.account_id = sqlc.arg(account_id)
  AND imap_folder_cursors.identity_key = sqlc.arg(identity_key);

-- name: MarkIMAPFolderCursorMissing :exec
UPDATE imap_folder_cursors
SET state = 'missing', next_uid = NULL, invalidated_at = NULL,
    invalidation_reason = NULL, version = version + 1, updated_at = now()
FROM accounts
WHERE accounts.id = imap_folder_cursors.account_id
  AND accounts.user_id = sqlc.arg(user_id)
  AND imap_folder_cursors.account_id = sqlc.arg(account_id)
  AND imap_folder_cursors.identity_key = sqlc.arg(identity_key);
