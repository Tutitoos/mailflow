package mail

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (repository *ThreadRepositoryStore) SearchMessages(ctx context.Context, user, account string, query SearchQuery, cursor *SearchCursor, limit int) (SearchPage, error) {
	userID, accountID, err := ownerAccountIDs(user, account)
	if err != nil || limit < 1 || limit > maxPageSize || !validSearchQuery(query) {
		return SearchPage{}, &SearchValidationError{Code: "invalid_query"}
	}
	params := dbgen.SearchMessageIDsParams{
		PageLimit: int32(limit + 1), SearchText: query.Text, AccountID: accountID, UserID: userID,
		AfterTime: optionalTime(query.After), BeforeTime: optionalTime(query.Before),
		RequireUnread: query.Unread, RequireStarred: query.Starred, RequireAttachment: query.HasAttachment,
		FromValues: append([]string{}, query.From...), ToValues: append([]string{}, query.To...),
		SubjectValues: append([]string{}, query.Subjects...), LabelValues: append([]string{}, query.Labels...),
		MailboxValues: append([]string{}, query.Mailboxes...),
	}
	if cursor != nil {
		cursorID, parseErr := databaseID(cursor.ID)
		if parseErr != nil || cursor.SentAt.IsZero() || math.IsNaN(float64(cursor.Rank)) || math.IsInf(float64(cursor.Rank), 0) || cursor.Rank < 0 {
			return SearchPage{}, &SearchValidationError{Code: "invalid_cursor"}
		}
		params.HasCursor = true
		params.CursorRank = cursor.Rank
		params.CursorSentAt = requiredTime(cursor.SentAt)
		params.CursorID = cursorID
	}
	queries := dbgen.New(repository.pool)
	rows, err := queries.SearchMessageIDs(ctx, params)
	if err != nil {
		return SearchPage{}, fmt.Errorf("search messages: %w", err)
	}
	selected := rows[:min(len(rows), limit)]
	ids := make([]pgtype.UUID, 0, len(selected))
	for _, row := range selected {
		ids = append(ids, row.ID)
	}
	messagesByID := make(map[string]Message, len(ids))
	if len(ids) > 0 {
		messageRows, queryErr := queries.GetMessagesByOwnerIDs(ctx, dbgen.GetMessagesByOwnerIDsParams{AccountID: accountID, UserID: userID, MessageIds: ids})
		if queryErr != nil {
			return SearchPage{}, fmt.Errorf("load search messages: %w", queryErr)
		}
		addressRows, queryErr := queries.ListAddressesForMessages(ctx, dbgen.ListAddressesForMessagesParams{MessageIds: ids, AccountID: accountID, UserID: userID})
		if queryErr != nil {
			return SearchPage{}, fmt.Errorf("load search addresses: %w", queryErr)
		}
		attachmentRows, queryErr := queries.ListAttachmentsForMessages(ctx, dbgen.ListAttachmentsForMessagesParams{MessageIds: ids, AccountID: accountID, UserID: userID})
		if queryErr != nil {
			return SearchPage{}, fmt.Errorf("load search attachments: %w", queryErr)
		}
		for _, row := range messageRows {
			message := mapMessage(row)
			messagesByID[message.ID] = message
		}
		for _, row := range addressRows {
			id := uuid.UUID(row.MessageID.Bytes).String()
			message := messagesByID[id]
			message.Addresses = append(message.Addresses, mapMessageAddress(row))
			messagesByID[id] = message
		}
		for _, row := range attachmentRows {
			id := uuid.UUID(row.MessageID.Bytes).String()
			message := messagesByID[id]
			message.Attachments = append(message.Attachments, mapAttachment(row))
			messagesByID[id] = message
		}
	}
	page := SearchPage{Items: make([]SearchHit, 0, len(selected))}
	for _, row := range selected {
		id := uuid.UUID(row.ID.Bytes).String()
		message, found := messagesByID[id]
		if !found {
			return SearchPage{}, fmt.Errorf("load search messages: result disappeared")
		}
		page.Items = append(page.Items, SearchHit{Message: message, Rank: row.Rank})
	}
	if len(rows) > limit {
		last := page.Items[len(page.Items)-1]
		page.Next = &SearchCursor{Rank: last.Rank, SentAt: last.Message.SentAt, ID: last.Message.ID}
	}
	return page, nil
}

func validSearchQuery(query SearchQuery) bool {
	if !utf8.ValidString(query.Text) || len(query.Text) > maxSearchLength || (query.After != nil && query.Before != nil && !query.After.Before(*query.Before)) {
		return false
	}
	values := [][]string{query.From, query.To, query.Subjects, query.Labels, query.Mailboxes}
	count := 0
	for _, group := range values {
		count += len(group)
		for _, value := range group {
			if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || len(value) > 1024 {
				return false
			}
		}
	}
	if count > 100 {
		return false
	}
	return strings.TrimSpace(query.Text) != "" || count > 0 || query.After != nil || query.Before != nil || query.HasAttachment || query.Unread || query.Starred
}
