-- name: SearchMessageIDs :many
WITH ranked AS (
  SELECT
    messages.id,
    messages.sent_at,
    CASE
      WHEN sqlc.arg(search_text)::text = '' THEN 0::real
      ELSE greatest(
        ts_rank_cd(messages.search_vector, websearch_to_tsquery('simple', sqlc.arg(search_text))),
        similarity(messages.subject, sqlc.arg(search_text)) * 0.2
      )::real
    END AS rank
  FROM messages
  JOIN accounts ON accounts.id = messages.account_id
  WHERE messages.account_id = sqlc.arg(account_id)
    AND accounts.user_id = sqlc.arg(user_id)
    AND (
      sqlc.arg(search_text)::text = ''
      OR messages.search_vector @@ websearch_to_tsquery('simple', sqlc.arg(search_text))
      OR messages.subject % sqlc.arg(search_text)
    )
    AND (sqlc.narg(after_time)::timestamptz IS NULL OR messages.sent_at >= sqlc.narg(after_time))
    AND (sqlc.narg(before_time)::timestamptz IS NULL OR messages.sent_at < sqlc.narg(before_time))
    AND (NOT sqlc.arg(require_unread)::boolean OR NOT messages.is_read)
    AND (NOT sqlc.arg(require_starred)::boolean OR messages.is_starred)
    AND (
      NOT sqlc.arg(require_attachment)::boolean
      OR EXISTS (
        SELECT 1 FROM message_attachments
        WHERE message_attachments.message_id = messages.id
          AND message_attachments.account_id = messages.account_id
      )
    )
    AND (
      cardinality(sqlc.arg(from_values)::text[]) = 0
      OR NOT EXISTS (
        SELECT 1 FROM unnest(sqlc.arg(from_values)::text[]) AS requested(value)
        WHERE NOT EXISTS (
          SELECT 1 FROM message_addresses
          WHERE message_addresses.message_id = messages.id
            AND message_addresses.account_id = messages.account_id
            AND message_addresses.role IN ('from', 'sender')
            AND strpos(lower(message_addresses.address), lower(requested.value)) > 0
        )
      )
    )
    AND (
      cardinality(sqlc.arg(to_values)::text[]) = 0
      OR NOT EXISTS (
        SELECT 1 FROM unnest(sqlc.arg(to_values)::text[]) AS requested(value)
        WHERE NOT EXISTS (
          SELECT 1 FROM message_addresses
          WHERE message_addresses.message_id = messages.id
            AND message_addresses.account_id = messages.account_id
            AND message_addresses.role IN ('to', 'cc', 'bcc')
            AND strpos(lower(message_addresses.address), lower(requested.value)) > 0
        )
      )
    )
    AND (
      cardinality(sqlc.arg(subject_values)::text[]) = 0
      OR NOT EXISTS (
        SELECT 1 FROM unnest(sqlc.arg(subject_values)::text[]) AS requested(value)
        WHERE strpos(lower(messages.subject), lower(requested.value)) = 0
      )
    )
    AND (
      cardinality(sqlc.arg(label_values)::text[]) = 0
      OR NOT EXISTS (
        SELECT 1 FROM unnest(sqlc.arg(label_values)::text[]) AS requested(value)
        WHERE NOT EXISTS (
          SELECT 1
          FROM message_labels
          JOIN labels ON labels.id = message_labels.label_id AND labels.account_id = message_labels.account_id
          WHERE message_labels.message_id = messages.id
            AND message_labels.account_id = messages.account_id
            AND lower(requested.value) IN (lower(labels.remote_name), lower(coalesce(labels.local_name, '')), lower(coalesce(labels.remote_id, '')))
        )
      )
    )
    AND (
      cardinality(sqlc.arg(mailbox_values)::text[]) = 0
      OR NOT EXISTS (
        SELECT 1 FROM unnest(sqlc.arg(mailbox_values)::text[]) AS requested(value)
        WHERE NOT EXISTS (
          SELECT 1
          FROM message_mailboxes
          JOIN mailboxes ON mailboxes.id = message_mailboxes.mailbox_id AND mailboxes.account_id = message_mailboxes.account_id
          WHERE message_mailboxes.message_id = messages.id
            AND message_mailboxes.account_id = messages.account_id
            AND lower(requested.value) IN (lower(mailboxes.remote_id), lower(mailboxes.remote_name), lower(coalesce(mailboxes.local_name, '')), lower(coalesce(mailboxes.role, '')))
        )
      )
    )
)
SELECT id, sent_at, rank
FROM ranked
WHERE NOT sqlc.arg(has_cursor)::boolean
  OR rank < sqlc.arg(cursor_rank)::real
  OR (rank = sqlc.arg(cursor_rank)::real AND sent_at < sqlc.arg(cursor_sent_at)::timestamptz)
  OR (rank = sqlc.arg(cursor_rank)::real AND sent_at = sqlc.arg(cursor_sent_at)::timestamptz AND id < sqlc.arg(cursor_id)::uuid)
ORDER BY rank DESC, sent_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: GetMessagesByOwnerIDs :many
SELECT messages.*
FROM messages
JOIN accounts ON accounts.id = messages.account_id
WHERE messages.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND messages.id = ANY(sqlc.arg(message_ids)::uuid[]);
