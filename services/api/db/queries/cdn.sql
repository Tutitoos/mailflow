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

-- name: TouchAttachmentObject :exec
UPDATE cdn_objects
SET last_accessed_at = sqlc.arg(accessed_at), updated_at = sqlc.arg(accessed_at)
WHERE object_id = sqlc.arg(object_id) AND namespace = 'attachments' AND storage_status = 'cached';

-- name: ListExpiredCachedAttachmentObjects :many
SELECT * FROM cdn_objects
WHERE namespace = 'attachments'
  AND storage_status = 'cached'
  AND expires_at IS NOT NULL
  AND expires_at <= sqlc.arg(expired_at)
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
