-- name: CreateDraft :one
INSERT INTO drafts (
  id, account_id, subject, body_text, body_html_sanitized, remote_checkpoint_at,
  compose_mode, source_message_id
)
SELECT sqlc.arg(id), accounts.id, sqlc.arg(subject), sqlc.arg(body_text),
       sqlc.arg(body_html_sanitized), sqlc.arg(remote_checkpoint_at),
       sqlc.arg(compose_mode), sqlc.narg(source_message_id)
FROM accounts
WHERE accounts.id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.disabled_at IS NULL
  AND (
    sqlc.narg(source_message_id)::uuid IS NULL OR EXISTS (
      SELECT 1 FROM messages
      WHERE messages.id = sqlc.narg(source_message_id)
        AND messages.account_id = accounts.id
    )
  )
RETURNING drafts.*;

-- name: GetDraftByOwner :one
SELECT drafts.*
FROM drafts
JOIN accounts ON accounts.id = drafts.account_id
WHERE drafts.id = sqlc.arg(id)
  AND drafts.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id);

-- name: UpdateDraft :one
UPDATE drafts
SET subject = sqlc.arg(subject), body_text = sqlc.arg(body_text),
    body_html_sanitized = sqlc.arg(body_html_sanitized),
    compose_mode = sqlc.arg(compose_mode), source_message_id = sqlc.narg(source_message_id),
    local_revision = local_revision + 1, sync_status = 'queued',
    remote_checkpoint_at = sqlc.arg(remote_checkpoint_at), updated_at = now()
FROM accounts
WHERE drafts.id = sqlc.arg(id)
  AND drafts.account_id = sqlc.arg(account_id)
  AND drafts.local_revision = sqlc.arg(expected_revision)
  AND drafts.sync_status <> 'discarded'
  AND accounts.id = drafts.account_id
  AND accounts.user_id = sqlc.arg(user_id)
  AND (
    sqlc.narg(source_message_id)::uuid IS NULL OR EXISTS (
      SELECT 1 FROM messages
      WHERE messages.id = sqlc.narg(source_message_id)
        AND messages.account_id = drafts.account_id
    )
  )
RETURNING drafts.*;

-- name: DeleteDraftRecipients :exec
DELETE FROM draft_recipients WHERE draft_id = sqlc.arg(draft_id) AND account_id = sqlc.arg(account_id);

-- name: CreateDraftRecipient :exec
INSERT INTO draft_recipients (draft_id, account_id, role, position, display_name, address)
VALUES (sqlc.arg(draft_id), sqlc.arg(account_id), sqlc.arg(role), sqlc.arg(position), sqlc.narg(display_name), sqlc.arg(address));

-- name: ListDraftRecipients :many
SELECT * FROM draft_recipients
WHERE draft_id = sqlc.arg(draft_id) AND account_id = sqlc.arg(account_id)
ORDER BY CASE role WHEN 'to' THEN 0 WHEN 'cc' THEN 1 ELSE 2 END, position;

-- name: DeleteDraftAttachments :exec
DELETE FROM draft_attachments WHERE draft_id = sqlc.arg(draft_id) AND account_id = sqlc.arg(account_id);

-- name: CreateDraftAttachment :exec
INSERT INTO draft_attachments (draft_id, account_id, position, object_id, filename, media_type, size_bytes)
VALUES (sqlc.arg(draft_id), sqlc.arg(account_id), sqlc.arg(position), sqlc.arg(object_id), sqlc.narg(filename), sqlc.arg(media_type), sqlc.arg(size_bytes));

-- name: ListDraftAttachments :many
SELECT * FROM draft_attachments
WHERE draft_id = sqlc.arg(draft_id) AND account_id = sqlc.arg(account_id)
ORDER BY position;

-- name: LockDraftByOwner :one
SELECT drafts.*
FROM drafts
JOIN accounts ON accounts.id = drafts.account_id
WHERE drafts.id = sqlc.arg(id)
  AND drafts.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
FOR UPDATE OF drafts;

-- name: CheckpointDraftRemote :one
UPDATE drafts
SET remote_id = sqlc.arg(remote_id), remote_revision = sqlc.arg(remote_revision),
    synced_revision = sqlc.arg(synced_revision), sync_status = 'synced',
    last_remote_synced_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND account_id = sqlc.arg(account_id)
  AND local_revision = sqlc.arg(synced_revision)
  AND sync_status <> 'discarded'
RETURNING drafts.*;

-- name: MarkDraftConflict :one
UPDATE drafts
SET remote_id = COALESCE(sqlc.narg(remote_id), remote_id),
    remote_revision = COALESCE(sqlc.narg(remote_revision), remote_revision),
    sync_status = 'conflict', updated_at = now()
FROM accounts
WHERE drafts.id = sqlc.arg(id)
  AND drafts.account_id = sqlc.arg(account_id)
  AND drafts.sync_status <> 'discarded'
  AND accounts.id = drafts.account_id
  AND accounts.user_id = sqlc.arg(user_id)
RETURNING drafts.*;

-- name: DiscardDraft :one
UPDATE drafts
SET sync_status = 'discarded', discarded_at = now(), updated_at = now()
FROM accounts
WHERE drafts.id = sqlc.arg(id)
  AND drafts.account_id = sqlc.arg(account_id)
  AND drafts.sync_status <> 'discarded'
  AND accounts.id = drafts.account_id
  AND accounts.user_id = sqlc.arg(user_id)
RETURNING drafts.*;
