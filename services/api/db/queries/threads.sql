-- name: UpsertThread :one
INSERT INTO threads (
  id, account_id, remote_id, last_message_at,
  is_read, is_starred, is_important, category, deleted_at
)
SELECT
  sqlc.arg(id), owned.id, sqlc.arg(remote_id), sqlc.arg(last_message_at),
  sqlc.arg(is_read), sqlc.arg(is_starred), sqlc.arg(is_important),
  sqlc.arg(category), sqlc.narg(deleted_at)
FROM accounts AS owned
WHERE owned.id = sqlc.arg(account_id)
  AND owned.user_id = sqlc.arg(user_id)
  AND owned.disabled_at IS NULL
ON CONFLICT (account_id, remote_id) DO UPDATE SET
  last_message_at = EXCLUDED.last_message_at,
  is_read = EXCLUDED.is_read,
  is_starred = EXCLUDED.is_starred,
  is_important = EXCLUDED.is_important,
  category = EXCLUDED.category,
  deleted_at = EXCLUDED.deleted_at,
  updated_at = now()
RETURNING *;

-- name: GetThreadByOwner :one
SELECT threads.*
FROM threads
JOIN accounts ON accounts.id = threads.account_id
WHERE threads.id = $1
  AND threads.account_id = $2
  AND accounts.user_id = $3;

-- name: ListThreadsFirstPage :many
SELECT threads.*
FROM threads
JOIN accounts ON accounts.id = threads.account_id
WHERE threads.account_id = $1
  AND accounts.user_id = $2
ORDER BY threads.last_message_at DESC, threads.id DESC
LIMIT $3;

-- name: ListThreadsAfter :many
SELECT threads.*
FROM threads
JOIN accounts ON accounts.id = threads.account_id
WHERE threads.account_id = $1
  AND accounts.user_id = $2
  AND (
    threads.last_message_at < sqlc.arg(cursor_last_message_at)
    OR (
      threads.last_message_at = sqlc.arg(cursor_last_message_at)
      AND threads.id < sqlc.arg(cursor_id)::uuid
    )
  )
ORDER BY threads.last_message_at DESC, threads.id DESC
LIMIT sqlc.arg(page_limit);

-- name: GetExistingMessageThread :one
SELECT thread_id
FROM messages
WHERE account_id = $1 AND remote_id = $2;

-- name: LockAccountMailState :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(account_id)::text, 0));

-- name: UpsertMessage :one
INSERT INTO messages (
  id, thread_id, account_id, remote_id, message_id, references_header,
  in_reply_to, sender, recipients, subject, body_text, body_html_sanitized,
  sent_at, is_read, is_starred, is_important, deleted_at, content_updated_at
)
SELECT
  sqlc.arg(id), owned_thread.id, owned_thread.account_id, sqlc.arg(remote_id),
  sqlc.narg(message_id), sqlc.arg(references_header), sqlc.arg(in_reply_to),
  '{}'::jsonb, '[]'::jsonb, sqlc.arg(subject), sqlc.arg(body_text),
  sqlc.arg(body_html_sanitized), sqlc.arg(sent_at), sqlc.arg(is_read),
  sqlc.arg(is_starred), sqlc.arg(is_important), sqlc.narg(deleted_at), now()
FROM threads AS owned_thread
JOIN accounts ON accounts.id = owned_thread.account_id
WHERE owned_thread.id = sqlc.arg(thread_id)
  AND owned_thread.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.disabled_at IS NULL
ON CONFLICT (account_id, remote_id) DO UPDATE SET
  thread_id = EXCLUDED.thread_id,
  message_id = EXCLUDED.message_id,
  references_header = EXCLUDED.references_header,
  in_reply_to = EXCLUDED.in_reply_to,
  subject = EXCLUDED.subject,
  body_text = EXCLUDED.body_text,
  body_html_sanitized = EXCLUDED.body_html_sanitized,
  content_updated_at = now(),
  sent_at = EXCLUDED.sent_at,
  is_read = EXCLUDED.is_read,
  is_starred = EXCLUDED.is_starred,
  is_important = EXCLUDED.is_important,
  deleted_at = EXCLUDED.deleted_at,
  updated_at = now()
RETURNING *;

-- name: DeleteMessageAttachmentsFromPosition :exec
DELETE FROM message_attachments
WHERE message_id = sqlc.arg(message_id)
  AND account_id = sqlc.arg(account_id)
  AND position >= sqlc.arg(from_position);

-- name: UpsertMessageAttachment :one
INSERT INTO message_attachments (
  id, message_id, account_id, position, remote_id, filename,
  media_type, disposition, content_id, size_bytes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (message_id, position) DO UPDATE SET
  remote_id = EXCLUDED.remote_id,
  filename = EXCLUDED.filename,
  media_type = EXCLUDED.media_type,
  disposition = EXCLUDED.disposition,
  content_id = EXCLUDED.content_id,
  size_bytes = EXCLUDED.size_bytes,
  updated_at = now()
RETURNING *;

-- name: DeleteMessageAddresses :exec
DELETE FROM message_addresses
WHERE message_id = $1 AND account_id = $2;

-- name: CreateMessageAddress :exec
INSERT INTO message_addresses (
  message_id, account_id, role, position, display_name, address
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListMessagesFirstPage :many
SELECT messages.*
FROM messages
JOIN threads ON threads.id = messages.thread_id AND threads.account_id = messages.account_id
JOIN accounts ON accounts.id = messages.account_id
WHERE messages.thread_id = $1
  AND messages.account_id = $2
  AND accounts.user_id = $3
ORDER BY messages.sent_at, messages.id
LIMIT $4;

-- name: ListMessagesAfter :many
SELECT messages.*
FROM messages
JOIN threads ON threads.id = messages.thread_id AND threads.account_id = messages.account_id
JOIN accounts ON accounts.id = messages.account_id
WHERE messages.thread_id = $1
  AND messages.account_id = $2
  AND accounts.user_id = $3
  AND (
    messages.sent_at > sqlc.arg(cursor_sent_at)
    OR (
      messages.sent_at = sqlc.arg(cursor_sent_at)
      AND messages.id > sqlc.arg(cursor_id)::uuid
    )
  )
ORDER BY messages.sent_at, messages.id
LIMIT sqlc.arg(page_limit);

-- name: ListAddressesForMessages :many
SELECT message_addresses.*
FROM message_addresses
JOIN accounts ON accounts.id = message_addresses.account_id
WHERE message_addresses.message_id = ANY(sqlc.arg(message_ids)::uuid[])
  AND message_addresses.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
ORDER BY message_addresses.message_id, message_addresses.role, message_addresses.position;

-- name: ListAttachmentsForMessages :many
SELECT message_attachments.*
FROM message_attachments
JOIN accounts ON accounts.id = message_attachments.account_id
WHERE message_attachments.message_id = ANY(sqlc.arg(message_ids)::uuid[])
  AND message_attachments.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
ORDER BY message_attachments.message_id, message_attachments.position;

-- name: UpdateMessagesStateByThread :exec
UPDATE messages
SET
  is_read = COALESCE(sqlc.narg(is_read), is_read),
  is_starred = COALESCE(sqlc.narg(is_starred), is_starred),
  is_important = COALESCE(sqlc.narg(is_important), is_important),
  deleted_at = CASE
    WHEN sqlc.narg(is_deleted)::boolean IS NULL THEN deleted_at
    WHEN sqlc.narg(is_deleted)::boolean THEN now()
    ELSE NULL
  END,
  updated_at = now()
WHERE thread_id = sqlc.arg(thread_id)
  AND account_id = sqlc.arg(account_id);

-- name: UpdateThreadState :one
UPDATE threads
SET
  is_read = COALESCE(sqlc.narg(is_read), is_read),
  is_starred = COALESCE(sqlc.narg(is_starred), is_starred),
  is_important = COALESCE(sqlc.narg(is_important), is_important),
  deleted_at = CASE
    WHEN sqlc.narg(is_deleted)::boolean IS NULL THEN deleted_at
    WHEN sqlc.narg(is_deleted)::boolean THEN now()
    ELSE NULL
  END,
  updated_at = now()
FROM accounts
WHERE threads.id = sqlc.arg(id)
  AND threads.account_id = sqlc.arg(account_id)
  AND accounts.id = threads.account_id
  AND accounts.user_id = sqlc.arg(user_id)
RETURNING threads.*;

-- name: UpdateMessageState :one
UPDATE messages
SET
  is_read = COALESCE(sqlc.narg(is_read), is_read),
  is_starred = COALESCE(sqlc.narg(is_starred), is_starred),
  is_important = COALESCE(sqlc.narg(is_important), is_important),
  deleted_at = CASE
    WHEN sqlc.narg(is_deleted)::boolean IS NULL THEN deleted_at
    WHEN sqlc.narg(is_deleted)::boolean THEN now()
    ELSE NULL
  END,
  updated_at = now()
FROM accounts
WHERE messages.id = sqlc.arg(id)
  AND messages.thread_id = sqlc.arg(thread_id)
  AND messages.account_id = sqlc.arg(account_id)
  AND accounts.id = messages.account_id
  AND accounts.user_id = sqlc.arg(user_id)
RETURNING messages.*;

-- name: RefreshThreadSummary :one
UPDATE threads
SET
  last_message_at = COALESCE(summary.last_message_at, threads.last_message_at),
  message_count = summary.message_count,
  unread_count = summary.unread_count,
  is_read = summary.unread_count = 0,
  is_starred = COALESCE(summary.is_starred, false),
  is_important = COALESCE(summary.is_important, false),
  deleted_at = CASE WHEN COALESCE(summary.all_deleted, false) THEN summary.deleted_at ELSE NULL END,
  updated_at = now()
FROM (
  SELECT
    count(*)::integer AS message_count,
    count(*) FILTER (WHERE NOT is_read)::integer AS unread_count,
    bool_or(is_starred) AS is_starred,
    bool_or(is_important) AS is_important,
    bool_and(deleted_at IS NOT NULL) AS all_deleted,
    max(deleted_at) AS deleted_at,
    max(sent_at) AS last_message_at
  FROM messages
  WHERE thread_id = sqlc.arg(thread_id)
    AND account_id = sqlc.arg(account_id)
) AS summary
WHERE threads.id = sqlc.arg(thread_id)
  AND threads.account_id = sqlc.arg(account_id)
RETURNING threads.*;
