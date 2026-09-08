-- name: UpsertIMAPMessageLocation :one
INSERT INTO imap_message_locations (
  message_id, mailbox_id, account_id, uid_validity, uid
)
SELECT
  sqlc.arg(message_id), mailboxes.id, sqlc.arg(account_id),
  sqlc.arg(uid_validity), sqlc.arg(uid)
FROM mailboxes
JOIN accounts ON accounts.id = mailboxes.account_id
WHERE mailboxes.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.provider = 'imap'
  AND accounts.disabled_at IS NULL
  AND mailboxes.remote_id = sqlc.arg(mailbox_remote_id)
ON CONFLICT (message_id, mailbox_id) DO UPDATE SET
  uid_validity = EXCLUDED.uid_validity,
  uid = EXCLUDED.uid,
  updated_at = now()
RETURNING imap_message_locations.*;

-- name: LinkMessageMailboxByRemoteID :execrows
INSERT INTO message_mailboxes (message_id, mailbox_id, account_id)
SELECT sqlc.arg(message_id), mailboxes.id, sqlc.arg(account_id)
FROM mailboxes
JOIN accounts ON accounts.id = mailboxes.account_id
WHERE mailboxes.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.disabled_at IS NULL
  AND mailboxes.remote_id = sqlc.arg(mailbox_remote_id)
ON CONFLICT (message_id, mailbox_id) DO NOTHING;

-- name: ListIMAPMessageLocations :many
SELECT
  messages.remote_id AS message_remote_id,
  mailboxes.remote_id AS mailbox_remote_id,
  imap_folder_cursors.wire_name,
  mailboxes.role,
  imap_message_locations.uid_validity,
  imap_message_locations.uid
FROM imap_message_locations
JOIN messages
  ON messages.id = imap_message_locations.message_id
  AND messages.account_id = imap_message_locations.account_id
JOIN mailboxes
  ON mailboxes.id = imap_message_locations.mailbox_id
  AND mailboxes.account_id = imap_message_locations.account_id
JOIN imap_folder_cursors
  ON imap_folder_cursors.mailbox_id = mailboxes.id
  AND imap_folder_cursors.account_id = mailboxes.account_id
JOIN accounts ON accounts.id = imap_message_locations.account_id
WHERE imap_message_locations.account_id = sqlc.arg(account_id)
  AND accounts.user_id = sqlc.arg(user_id)
  AND accounts.provider = 'imap'
  AND accounts.disabled_at IS NULL
  AND messages.remote_id = ANY(sqlc.arg(message_remote_ids)::text[])
ORDER BY messages.remote_id, mailboxes.role NULLS LAST, mailboxes.remote_name;

-- name: DeleteIMAPMessageLocation :execrows
DELETE FROM imap_message_locations
USING messages, mailboxes, accounts
WHERE imap_message_locations.message_id = messages.id
  AND imap_message_locations.mailbox_id = mailboxes.id
  AND imap_message_locations.account_id = messages.account_id
  AND imap_message_locations.account_id = mailboxes.account_id
  AND accounts.id = imap_message_locations.account_id
  AND accounts.user_id = sqlc.arg(user_id)
  AND imap_message_locations.account_id = sqlc.arg(account_id)
  AND messages.remote_id = sqlc.arg(message_remote_id)
  AND mailboxes.remote_id = sqlc.arg(mailbox_remote_id)
  AND imap_message_locations.uid_validity = sqlc.arg(uid_validity)
  AND imap_message_locations.uid = sqlc.arg(uid);
