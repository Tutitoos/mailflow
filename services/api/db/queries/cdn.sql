-- name: UpsertAttachmentObject :one
INSERT INTO cdn_objects (
  object_id, namespace, account_id, recovery_reference, filename,
  media_type, size_bytes, etag, storage_status, expires_at, stored_at, updated_at
)
SELECT sqlc.arg(object_id), 'attachments', accounts.id, sqlc.narg(recovery_reference),
       sqlc.narg(filename), sqlc.arg(media_type), sqlc.arg(size_bytes), sqlc.arg(etag),
       'cached', sqlc.arg(expires_at), sqlc.arg(stored_at), sqlc.arg(stored_at)
FROM accounts
WHERE accounts.id = sqlc.arg(account_id) AND accounts.user_id = sqlc.arg(user_id)
ON CONFLICT (object_id) DO UPDATE SET
  recovery_reference = EXCLUDED.recovery_reference,
  filename = EXCLUDED.filename,
  media_type = EXCLUDED.media_type,
  size_bytes = EXCLUDED.size_bytes,
  etag = EXCLUDED.etag,
  storage_status = 'cached',
  expires_at = EXCLUDED.expires_at,
  stored_at = EXCLUDED.stored_at,
  updated_at = EXCLUDED.updated_at
WHERE cdn_objects.namespace = 'attachments'
  AND cdn_objects.account_id = EXCLUDED.account_id
RETURNING cdn_objects.*;

-- name: GetAttachmentObjectForUser :one
SELECT cdn_objects.* FROM cdn_objects
JOIN accounts ON cdn_objects.account_id = accounts.id
WHERE cdn_objects.object_id = sqlc.arg(object_id)
  AND cdn_objects.namespace = 'attachments'
  AND accounts.user_id = sqlc.arg(user_id);

-- name: GetMessageAttachmentForUser :one
SELECT message_attachments.id, message_attachments.account_id,
       message_attachments.remote_id, message_attachments.filename,
       message_attachments.media_type, message_attachments.size_bytes,
       message_attachments.cached_object_id,
       messages.remote_id AS message_remote_id,
       accounts.provider
FROM message_attachments
JOIN messages ON messages.id = message_attachments.message_id
  AND messages.account_id = message_attachments.account_id
JOIN accounts ON accounts.id = message_attachments.account_id
WHERE message_attachments.id = sqlc.arg(attachment_id)
  AND accounts.user_id = sqlc.arg(user_id);

-- name: LinkMessageAttachmentObject :execrows
UPDATE message_attachments
SET cached_object_id = sqlc.arg(object_id), updated_at = now()
FROM accounts
WHERE message_attachments.id = sqlc.arg(attachment_id)
  AND accounts.id = message_attachments.account_id
  AND accounts.user_id = sqlc.arg(user_id)
  AND EXISTS (
    SELECT 1 FROM cdn_objects
    WHERE cdn_objects.object_id = sqlc.arg(object_id)
      AND cdn_objects.namespace = 'attachments'
      AND cdn_objects.account_id = message_attachments.account_id
      AND cdn_objects.storage_status = 'cached'
  );

-- name: TouchAttachmentObject :exec
UPDATE cdn_objects
SET last_accessed_at = sqlc.arg(accessed_at), expires_at = sqlc.arg(expires_at), updated_at = sqlc.arg(accessed_at)
WHERE object_id = sqlc.arg(object_id) AND namespace = 'attachments' AND storage_status = 'cached';

-- name: ListExpiredCachedAttachmentObjects :many
SELECT * FROM cdn_objects
WHERE namespace = 'attachments'
  AND storage_status = 'cached'
  AND expires_at IS NOT NULL
  AND expires_at <= sqlc.arg(expired_at)
  AND NOT EXISTS (
    SELECT 1 FROM draft_attachments
    JOIN drafts ON drafts.id = draft_attachments.draft_id
      AND drafts.account_id = draft_attachments.account_id
    WHERE draft_attachments.object_id = cdn_objects.object_id
      AND drafts.sync_status <> 'discarded'
  )
ORDER BY expires_at, object_id
LIMIT sqlc.arg(batch_size);

-- name: MarkAttachmentObjectMissing :exec
UPDATE cdn_objects
SET storage_status = 'missing', updated_at = sqlc.arg(updated_at)
WHERE object_id = sqlc.arg(object_id) AND namespace = 'attachments';

-- name: ListOrphanedAttachmentObjects :many
SELECT * FROM cdn_objects
WHERE namespace = 'attachments'
  AND storage_status = 'missing'
  AND cdn_objects.updated_at <= sqlc.arg(orphaned_before)
  AND NOT EXISTS (
    SELECT 1 FROM message_attachments WHERE message_attachments.cached_object_id = cdn_objects.object_id
  )
  AND NOT EXISTS (
    SELECT 1 FROM draft_attachments WHERE draft_attachments.object_id = cdn_objects.object_id
  )
ORDER BY cdn_objects.updated_at, cdn_objects.object_id
LIMIT sqlc.arg(batch_size);

-- name: DeleteOrphanedAttachmentObject :execrows
DELETE FROM cdn_objects
WHERE cdn_objects.object_id = sqlc.arg(object_id)
  AND namespace = 'attachments'
  AND storage_status = 'missing'
  AND NOT EXISTS (
    SELECT 1 FROM message_attachments WHERE message_attachments.cached_object_id = cdn_objects.object_id
  )
  AND NOT EXISTS (
    SELECT 1 FROM draft_attachments WHERE draft_attachments.object_id = cdn_objects.object_id
  );
