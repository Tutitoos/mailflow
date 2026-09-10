-- name: DeleteMessageMailboxMemberships :execrows
DELETE FROM message_mailboxes
USING accounts
WHERE message_mailboxes.message_id = sqlc.arg(message_id)
  AND message_mailboxes.account_id = sqlc.arg(account_id)
  AND accounts.id = message_mailboxes.account_id
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.disabled_at IS NULL;

-- name: DeleteMessageLabelMemberships :execrows
DELETE FROM message_labels
USING accounts
WHERE message_labels.message_id = sqlc.arg(message_id)
  AND message_labels.account_id = sqlc.arg(account_id)
  AND accounts.id = message_labels.account_id
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.disabled_at IS NULL;

-- name: LinkMessageLabelByRemoteID :execrows
INSERT INTO message_labels (message_id, label_id, account_id)
SELECT sqlc.arg(message_id), labels.id, sqlc.arg(account_id)
FROM labels
JOIN accounts ON accounts.id = labels.account_id
WHERE labels.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.disabled_at IS NULL
  AND labels.remote_id = sqlc.arg(label_remote_id)
ON CONFLICT (message_id, label_id) DO NOTHING;

-- name: LinkMessageLabelByCategory :execrows
INSERT INTO message_labels (message_id, label_id, account_id)
SELECT sqlc.arg(message_id), labels.id, sqlc.arg(account_id)
FROM labels
JOIN accounts ON accounts.id = labels.account_id
WHERE labels.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.disabled_at IS NULL
  AND labels.kind = 'category'
  AND labels.category = sqlc.arg(category)
ON CONFLICT (message_id, label_id) DO NOTHING;
