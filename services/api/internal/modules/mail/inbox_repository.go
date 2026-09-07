package mail

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ListInbox returns a stable cursor page for one account and category. The
// lateral joins are bounded by indexed thread/message keys and avoid N+1
// queries while keeping full message content out of the API response.
func (repository *ThreadRepositoryStore) ListInbox(ctx context.Context, user, account string, category Category, cursor *ThreadCursor, limit int) (InboxPage, error) {
	userID, accountID, err := ownerAccountIDs(user, account)
	if err != nil || !validCategory(category) || limit < 1 || limit > maxPageSize {
		return InboxPage{}, ErrInvalidThread
	}

	args := []any{accountID, userID, string(category), int32(limit + 1)}
	cursorClause := ""
	if cursor != nil {
		cursorID, parseErr := databaseID(cursor.ID)
		if parseErr != nil || cursor.LastMessageAt.IsZero() {
			return InboxPage{}, ErrInvalidThread
		}
		cursorClause = `
  AND (threads.last_message_at < $5 OR (threads.last_message_at = $5 AND threads.id < $6))`
		args = append(args, requiredTime(cursor.LastMessageAt), cursorID)
	}

	rows, err := repository.pool.Query(ctx, `
SELECT
  threads.id,
  threads.account_id,
  COALESCE(sender.display_name, sender.address, ''),
  COALESCE(sender.address, ''),
  latest.subject,
  left(regexp_replace(latest.body_text, '[[:space:]]+', ' ', 'g'), 240),
  threads.last_message_at,
  threads.is_read,
  threads.is_starred,
  threads.is_important,
  threads.category,
  threads.message_count,
  COALESCE(attachments.total, 0)::integer
FROM threads
JOIN accounts ON accounts.id = threads.account_id
JOIN LATERAL (
  SELECT messages.id, messages.subject, messages.body_text
  FROM messages
  WHERE messages.thread_id = threads.id
    AND messages.account_id = threads.account_id
    AND messages.deleted_at IS NULL
  ORDER BY messages.sent_at DESC, messages.id DESC
  LIMIT 1
) AS latest ON true
LEFT JOIN LATERAL (
  SELECT message_addresses.display_name, message_addresses.address
  FROM message_addresses
  WHERE message_addresses.message_id = latest.id
    AND message_addresses.account_id = threads.account_id
    AND message_addresses.role IN ('from', 'sender')
  ORDER BY CASE message_addresses.role WHEN 'from' THEN 0 ELSE 1 END, message_addresses.position
  LIMIT 1
) AS sender ON true
LEFT JOIN LATERAL (
  SELECT count(*) AS total
  FROM message_attachments
  WHERE message_attachments.message_id = latest.id
    AND message_attachments.account_id = threads.account_id
) AS attachments ON true
WHERE threads.account_id = $1
  AND accounts.user_id = $2
  AND accounts.disabled_at IS NULL
  AND threads.deleted_at IS NULL
  AND threads.category = $3`+cursorClause+`
ORDER BY threads.last_message_at DESC, threads.id DESC
LIMIT $4`, args...)
	if err != nil {
		return InboxPage{}, fmt.Errorf("list inbox: %w", err)
	}
	defer rows.Close()

	items := make([]InboxThread, 0, limit+1)
	for rows.Next() {
		var item InboxThread
		var id, itemAccountID uuid.UUID
		var categoryName string
		if err := rows.Scan(
			&id, &itemAccountID, &item.SenderName, &item.SenderAddress,
			&item.Subject, &item.Preview, &item.LastMessageAt, &item.IsRead,
			&item.IsStarred, &item.IsImportant, &categoryName,
			&item.MessageCount, &item.AttachmentCount,
		); err != nil {
			return InboxPage{}, fmt.Errorf("scan inbox: %w", err)
		}
		item.ID = id.String()
		item.AccountID = itemAccountID.String()
		item.Category = Category(categoryName)
		item.Preview = strings.TrimSpace(item.Preview)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return InboxPage{}, fmt.Errorf("read inbox: %w", err)
	}

	page := InboxPage{Items: items[:min(len(items), limit)]}
	if len(items) > limit {
		last := page.Items[len(page.Items)-1]
		page.Next = &ThreadCursor{LastMessageAt: last.LastMessageAt, ID: last.ID}
	}
	return page, nil
}

var _ interface {
	ListInbox(context.Context, string, string, Category, *ThreadCursor, int) (InboxPage, error)
} = (*ThreadRepositoryStore)(nil)
