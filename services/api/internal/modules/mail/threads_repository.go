package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxPageSize = 100

type ThreadRepositoryStore struct {
	pool *pgxpool.Pool
}

func NewThreadRepository(pool *pgxpool.Pool) *ThreadRepositoryStore {
	return &ThreadRepositoryStore{pool: pool}
}

func (repository *ThreadRepositoryStore) UpsertThread(ctx context.Context, input UpsertThreadInput) (Thread, error) {
	userID, accountID, err := ownerAccountIDs(input.UserID, input.AccountID)
	if err != nil || !boundedText(input.RemoteID, 512) || input.LastMessageAt.IsZero() || !validCategory(input.Category) {
		return Thread{}, ErrInvalidThread
	}
	id, err := newDatabaseID()
	if err != nil {
		return Thread{}, err
	}
	row, err := dbgen.New(repository.pool).UpsertThread(ctx, dbgen.UpsertThreadParams{
		ID: id, RemoteID: strings.TrimSpace(input.RemoteID), LastMessageAt: requiredTime(input.LastMessageAt),
		IsRead: input.IsRead, IsStarred: input.IsStarred, IsImportant: input.IsImportant,
		Category: string(input.Category), DeletedAt: optionalTime(input.DeletedAt), AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Thread{}, ErrThreadNotFound
	}
	if err != nil {
		return Thread{}, fmt.Errorf("upsert thread: %w", err)
	}
	return mapThread(row), nil
}

func (repository *ThreadRepositoryStore) GetThread(ctx context.Context, user, account, thread string) (Thread, error) {
	userID, accountID, threadID, err := scopedResourceIDs(user, account, thread)
	if err != nil {
		return Thread{}, ErrInvalidThread
	}
	row, err := dbgen.New(repository.pool).GetThreadByOwner(ctx, dbgen.GetThreadByOwnerParams{ID: threadID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Thread{}, ErrThreadNotFound
	}
	if err != nil {
		return Thread{}, fmt.Errorf("get thread: %w", err)
	}
	return mapThread(row), nil
}

func (repository *ThreadRepositoryStore) ListThreads(ctx context.Context, user, account string, cursor *ThreadCursor, limit int) (ThreadPage, error) {
	userID, accountID, err := ownerAccountIDs(user, account)
	if err != nil || limit < 1 || limit > maxPageSize {
		return ThreadPage{}, ErrInvalidThread
	}
	queries := dbgen.New(repository.pool)
	var rows []dbgen.Thread
	if cursor == nil {
		rows, err = queries.ListThreadsFirstPage(ctx, dbgen.ListThreadsFirstPageParams{AccountID: accountID, UserID: userID, Limit: int32(limit + 1)})
	} else {
		cursorID, parseErr := databaseID(cursor.ID)
		if parseErr != nil || cursor.LastMessageAt.IsZero() {
			return ThreadPage{}, ErrInvalidThread
		}
		rows, err = queries.ListThreadsAfter(ctx, dbgen.ListThreadsAfterParams{
			AccountID: accountID, UserID: userID, CursorLastMessageAt: requiredTime(cursor.LastMessageAt),
			CursorID: cursorID, PageLimit: int32(limit + 1),
		})
	}
	if err != nil {
		return ThreadPage{}, fmt.Errorf("list threads: %w", err)
	}
	page := ThreadPage{Items: make([]Thread, 0, min(len(rows), limit))}
	for _, row := range rows[:min(len(rows), limit)] {
		page.Items = append(page.Items, mapThread(row))
	}
	if len(rows) > limit {
		last := page.Items[len(page.Items)-1]
		page.Next = &ThreadCursor{LastMessageAt: last.LastMessageAt, ID: last.ID}
	}
	return page, nil
}

func (repository *ThreadRepositoryStore) UpsertMessage(ctx context.Context, input UpsertMessageInput) (Message, error) {
	userID, accountID, threadID, err := scopedResourceIDs(input.UserID, input.AccountID, input.ThreadID)
	if err != nil || !validMessageInput(input) {
		return Message{}, ErrInvalidMessage
	}
	input.BodyHTML, err = sanitizeHTML(input.BodyHTML)
	if err != nil || len(input.BodyHTML) > 10<<20 {
		return Message{}, ErrInvalidMessage
	}
	id, err := newDatabaseID()
	if err != nil {
		return Message{}, err
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Message{}, fmt.Errorf("begin message transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	if err := queries.LockAccountMailState(ctx, uuid.UUID(accountID.Bytes).String()); err != nil {
		return Message{}, fmt.Errorf("lock account mail state: %w", err)
	}

	oldThreadID, oldThreadErr := queries.GetExistingMessageThread(ctx, dbgen.GetExistingMessageThreadParams{AccountID: accountID, RemoteID: strings.TrimSpace(input.RemoteID)})
	if oldThreadErr != nil && !errors.Is(oldThreadErr, pgx.ErrNoRows) {
		return Message{}, fmt.Errorf("find existing message: %w", oldThreadErr)
	}
	row, err := queries.UpsertMessage(ctx, dbgen.UpsertMessageParams{
		ID: id, RemoteID: strings.TrimSpace(input.RemoteID), MessageID: optionalText(input.MessageID),
		ReferencesHeader: append([]string{}, input.References...), InReplyTo: append([]string{}, input.InReplyTo...),
		Subject: input.Subject, BodyText: input.BodyText, BodyHtmlSanitized: input.BodyHTML,
		SentAt: requiredTime(input.SentAt),
		IsRead: input.IsRead, IsStarred: input.IsStarred, IsImportant: input.IsImportant,
		DeletedAt: optionalTime(input.DeletedAt), ThreadID: threadID, AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrThreadNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("upsert message: %w", err)
	}
	if err := queries.DeleteMessageAddresses(ctx, dbgen.DeleteMessageAddressesParams{MessageID: row.ID, AccountID: accountID}); err != nil {
		return Message{}, fmt.Errorf("replace message addresses: %w", err)
	}
	addresses := normalizeAddresses(input.Addresses)
	for _, address := range addresses {
		if err := queries.CreateMessageAddress(ctx, dbgen.CreateMessageAddressParams{
			MessageID: row.ID, AccountID: accountID, Role: string(address.Role), Position: address.Position,
			DisplayName: optionalText(pointerValue(address.DisplayName)), Address: address.Address,
		}); err != nil {
			return Message{}, fmt.Errorf("create message address: %w", err)
		}
	}
	if err := queries.DeleteMessageAttachmentsFromPosition(ctx, dbgen.DeleteMessageAttachmentsFromPositionParams{
		MessageID: row.ID, AccountID: accountID, FromPosition: int32(len(input.Attachments)),
	}); err != nil {
		return Message{}, fmt.Errorf("trim message attachments: %w", err)
	}
	attachments := make([]Attachment, 0, len(input.Attachments))
	for position, attachment := range input.Attachments {
		attachmentID, idErr := newDatabaseID()
		if idErr != nil {
			return Message{}, idErr
		}
		attachmentRow, attachmentErr := queries.UpsertMessageAttachment(ctx, dbgen.UpsertMessageAttachmentParams{
			ID: attachmentID, MessageID: row.ID, AccountID: accountID, Position: int32(position),
			RemoteID: optionalText(attachment.RemoteID), Filename: optionalText(attachment.Filename),
			MediaType: strings.ToLower(strings.TrimSpace(attachment.MediaType)), Disposition: attachment.Disposition,
			ContentID: optionalText(attachment.ContentID), SizeBytes: attachment.SizeBytes,
		})
		if attachmentErr != nil {
			return Message{}, fmt.Errorf("upsert message attachment: %w", attachmentErr)
		}
		attachments = append(attachments, mapAttachment(attachmentRow))
	}
	if _, err := queries.RefreshThreadSummary(ctx, dbgen.RefreshThreadSummaryParams{ThreadID: threadID, AccountID: accountID}); err != nil {
		return Message{}, fmt.Errorf("refresh message thread: %w", err)
	}
	if oldThreadErr == nil && oldThreadID != threadID {
		if _, err := queries.RefreshThreadSummary(ctx, dbgen.RefreshThreadSummaryParams{ThreadID: oldThreadID, AccountID: accountID}); err != nil {
			return Message{}, fmt.Errorf("refresh previous message thread: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, fmt.Errorf("commit message: %w", err)
	}
	message := mapMessage(row)
	message.Addresses = addresses
	message.Attachments = attachments
	return message, nil
}

func (repository *ThreadRepositoryStore) ListMessages(ctx context.Context, user, account, thread string, cursor *MessageCursor, limit int) (MessagePage, error) {
	userID, accountID, threadID, err := scopedResourceIDs(user, account, thread)
	if err != nil || limit < 1 || limit > maxPageSize {
		return MessagePage{}, ErrInvalidMessage
	}
	queries := dbgen.New(repository.pool)
	var rows []dbgen.Message
	if cursor == nil {
		rows, err = queries.ListMessagesFirstPage(ctx, dbgen.ListMessagesFirstPageParams{ThreadID: threadID, AccountID: accountID, UserID: userID, Limit: int32(limit + 1)})
	} else {
		cursorID, parseErr := databaseID(cursor.ID)
		if parseErr != nil || cursor.SentAt.IsZero() {
			return MessagePage{}, ErrInvalidMessage
		}
		rows, err = queries.ListMessagesAfter(ctx, dbgen.ListMessagesAfterParams{
			ThreadID: threadID, AccountID: accountID, UserID: userID,
			CursorSentAt: requiredTime(cursor.SentAt), CursorID: cursorID, PageLimit: int32(limit + 1),
		})
	}
	if err != nil {
		return MessagePage{}, fmt.Errorf("list messages: %w", err)
	}
	selected := rows[:min(len(rows), limit)]
	messageIDs := make([]pgtype.UUID, 0, len(selected))
	for _, row := range selected {
		messageIDs = append(messageIDs, row.ID)
	}
	addressesByMessage := make(map[string][]MessageAddress)
	attachmentsByMessage := make(map[string][]Attachment)
	if len(messageIDs) > 0 {
		addressRows, queryErr := queries.ListAddressesForMessages(ctx, dbgen.ListAddressesForMessagesParams{MessageIds: messageIDs, AccountID: accountID, UserID: userID})
		if queryErr != nil {
			return MessagePage{}, fmt.Errorf("list message addresses: %w", queryErr)
		}
		for _, row := range addressRows {
			key := uuid.UUID(row.MessageID.Bytes).String()
			addressesByMessage[key] = append(addressesByMessage[key], mapMessageAddress(row))
		}
		attachmentRows, queryErr := queries.ListAttachmentsForMessages(ctx, dbgen.ListAttachmentsForMessagesParams{MessageIds: messageIDs, AccountID: accountID, UserID: userID})
		if queryErr != nil {
			return MessagePage{}, fmt.Errorf("list message attachments: %w", queryErr)
		}
		for _, row := range attachmentRows {
			key := uuid.UUID(row.MessageID.Bytes).String()
			attachmentsByMessage[key] = append(attachmentsByMessage[key], mapAttachment(row))
		}
	}
	page := MessagePage{Items: make([]Message, 0, len(selected))}
	for _, row := range selected {
		message := mapMessage(row)
		message.Addresses = addressesByMessage[message.ID]
		message.Attachments = attachmentsByMessage[message.ID]
		page.Items = append(page.Items, message)
	}
	if len(rows) > limit {
		last := page.Items[len(page.Items)-1]
		page.Next = &MessageCursor{SentAt: last.SentAt, ID: last.ID}
	}
	return page, nil
}

func (repository *ThreadRepositoryStore) ApplyThreadState(ctx context.Context, user, account, thread string, patch StatePatch) (Thread, error) {
	userID, accountID, threadID, err := scopedResourceIDs(user, account, thread)
	if err != nil || !validPatch(patch) {
		return Thread{}, ErrInvalidThread
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Thread{}, fmt.Errorf("begin thread state transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	if err := queries.LockAccountMailState(ctx, uuid.UUID(accountID.Bytes).String()); err != nil {
		return Thread{}, fmt.Errorf("lock account mail state: %w", err)
	}
	if _, err := queries.GetThreadByOwner(ctx, dbgen.GetThreadByOwnerParams{ID: threadID, AccountID: accountID, UserID: userID}); errors.Is(err, pgx.ErrNoRows) {
		return Thread{}, ErrThreadNotFound
	} else if err != nil {
		return Thread{}, fmt.Errorf("authorize thread state: %w", err)
	}
	params := patchParams(patch)
	if err := queries.UpdateMessagesStateByThread(ctx, dbgen.UpdateMessagesStateByThreadParams{
		IsRead: params.read, IsStarred: params.starred, IsImportant: params.important,
		IsDeleted: params.deleted, ThreadID: threadID, AccountID: accountID,
	}); err != nil {
		return Thread{}, fmt.Errorf("update thread messages: %w", err)
	}
	row, err := queries.UpdateThreadState(ctx, dbgen.UpdateThreadStateParams{
		IsRead: params.read, IsStarred: params.starred, IsImportant: params.important,
		IsDeleted: params.deleted, ID: threadID, AccountID: accountID, UserID: userID,
	})
	if err != nil {
		return Thread{}, fmt.Errorf("update thread state: %w", err)
	}
	if row.MessageCount > 0 {
		row, err = queries.RefreshThreadSummary(ctx, dbgen.RefreshThreadSummaryParams{ThreadID: threadID, AccountID: accountID})
		if err != nil {
			return Thread{}, fmt.Errorf("refresh thread state: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Thread{}, fmt.Errorf("commit thread state: %w", err)
	}
	return mapThread(row), nil
}

func (repository *ThreadRepositoryStore) ApplyMessageState(ctx context.Context, user, account, thread, message string, patch StatePatch) (Message, error) {
	userID, accountID, threadID, err := scopedResourceIDs(user, account, thread)
	if err != nil || !validPatch(patch) {
		return Message{}, ErrInvalidMessage
	}
	messageID, err := databaseID(message)
	if err != nil {
		return Message{}, ErrInvalidMessage
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Message{}, fmt.Errorf("begin message state transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	if err := queries.LockAccountMailState(ctx, uuid.UUID(accountID.Bytes).String()); err != nil {
		return Message{}, fmt.Errorf("lock account mail state: %w", err)
	}
	params := patchParams(patch)
	row, err := queries.UpdateMessageState(ctx, dbgen.UpdateMessageStateParams{
		IsRead: params.read, IsStarred: params.starred, IsImportant: params.important,
		IsDeleted: params.deleted, ID: messageID, ThreadID: threadID, AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrMessageNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("update message state: %w", err)
	}
	if _, err := queries.RefreshThreadSummary(ctx, dbgen.RefreshThreadSummaryParams{ThreadID: threadID, AccountID: accountID}); err != nil {
		return Message{}, fmt.Errorf("refresh message state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, fmt.Errorf("commit message state: %w", err)
	}
	return mapMessage(row), nil
}

func validMessageInput(input UpsertMessageInput) bool {
	if !boundedText(input.RemoteID, 512) || input.SentAt.IsZero() || !utf8.ValidString(input.Subject) || !utf8.ValidString(input.BodyText) || !utf8.ValidString(input.BodyHTML) || len(strings.TrimSpace(input.MessageID)) > 998 || len(input.References) > 100 || len(input.InReplyTo) > 100 || len(input.Subject) > 1<<20 || len(input.BodyText) > 10<<20 || len(input.BodyHTML) > 10<<20 || len(input.Addresses) > 512 || len(input.Attachments) > 1024 {
		return false
	}
	for _, reference := range append(append([]string{}, input.References...), input.InReplyTo...) {
		if !boundedText(reference, 998) {
			return false
		}
	}
	for _, address := range input.Addresses {
		if !validAddressRole(address.Role) || !boundedText(address.Address, 1024) || !utf8.ValidString(address.Address) || !utf8.ValidString(address.DisplayName) || len(strings.TrimSpace(address.DisplayName)) > 256 {
			return false
		}
	}
	for _, attachment := range input.Attachments {
		if attachment.SizeBytes < 0 || !utf8.ValidString(attachment.RemoteID) || !utf8.ValidString(attachment.Filename) || !utf8.ValidString(attachment.ContentID) || len(strings.TrimSpace(attachment.RemoteID)) > 512 || len(strings.TrimSpace(attachment.Filename)) > 1024 || !boundedText(attachment.MediaType, 255) || (attachment.Disposition != "attachment" && attachment.Disposition != "inline") || len(strings.TrimSpace(attachment.ContentID)) > 998 {
			return false
		}
	}
	return true
}

func validAddressRole(role AddressRole) bool {
	switch role {
	case AddressFrom, AddressSender, AddressReplyTo, AddressTo, AddressCC, AddressBCC:
		return true
	default:
		return false
	}
}

func validPatch(patch StatePatch) bool {
	return patch.Read != nil || patch.Starred != nil || patch.Important != nil || patch.Deleted != nil
}

type stateParams struct {
	read      pgtype.Bool
	starred   pgtype.Bool
	important pgtype.Bool
	deleted   pgtype.Bool
}

func patchParams(patch StatePatch) stateParams {
	return stateParams{read: optionalBool(patch.Read), starred: optionalBool(patch.Starred), important: optionalBool(patch.Important), deleted: optionalBool(patch.Deleted)}
}

func optionalBool(value *bool) pgtype.Bool {
	if value == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *value, Valid: true}
}

func requiredTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func normalizeAddresses(inputs []MessageAddressInput) []MessageAddress {
	positions := make(map[AddressRole]int32)
	result := make([]MessageAddress, 0, len(inputs))
	for _, input := range inputs {
		role := input.Role
		position := positions[role]
		positions[role]++
		var displayName *string
		if trimmed := strings.TrimSpace(input.DisplayName); trimmed != "" {
			displayName = &trimmed
		}
		result = append(result, MessageAddress{Role: role, Position: position, DisplayName: displayName, Address: strings.TrimSpace(input.Address)})
	}
	return result
}

func mapThread(row dbgen.Thread) Thread {
	return Thread{
		ID: uuid.UUID(row.ID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(),
		RemoteID: row.RemoteID, LastMessageAt: row.LastMessageAt.Time.UTC(), IsRead: row.IsRead,
		IsStarred: row.IsStarred, IsImportant: row.IsImportant, Category: Category(row.Category),
		DeletedAt: timePointer(row.DeletedAt), MessageCount: row.MessageCount, UnreadCount: row.UnreadCount,
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}

func mapMessage(row dbgen.Message) Message {
	return Message{
		ID: uuid.UUID(row.ID.Bytes).String(), ThreadID: uuid.UUID(row.ThreadID.Bytes).String(),
		AccountID: uuid.UUID(row.AccountID.Bytes).String(), RemoteID: row.RemoteID,
		MessageID: textPointer(row.MessageID), References: append([]string(nil), row.ReferencesHeader...),
		InReplyTo: append([]string(nil), row.InReplyTo...), Subject: row.Subject,
		BodyText: row.BodyText, BodyHTML: row.BodyHtmlSanitized,
		SentAt: row.SentAt.Time.UTC(), IsRead: row.IsRead, IsStarred: row.IsStarred,
		IsImportant: row.IsImportant, DeletedAt: timePointer(row.DeletedAt),
		Addresses: []MessageAddress{}, Attachments: []Attachment{}, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}

func mapMessageAddress(row dbgen.MessageAddress) MessageAddress {
	return MessageAddress{Role: AddressRole(row.Role), Position: row.Position, DisplayName: textPointer(row.DisplayName), Address: row.Address}
}

func mapAttachment(row dbgen.MessageAttachment) Attachment {
	return Attachment{
		ID: uuid.UUID(row.ID.Bytes).String(), Position: row.Position, RemoteID: textPointer(row.RemoteID),
		Filename: textPointer(row.Filename), MediaType: row.MediaType, Disposition: row.Disposition,
		ContentID: textPointer(row.ContentID), SizeBytes: row.SizeBytes,
	}
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
