package mail

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type RemotePageWriter struct{}

func NewRemotePageWriter() *RemotePageWriter { return &RemotePageWriter{} }

func (writer *RemotePageWriter) ApplyGmailPage(ctx context.Context, tx pgx.Tx, user, account string, catalog CatalogPage, page ChangePage) error {
	userID, accountID, err := ownerAccountIDs(user, account)
	if err != nil {
		return ErrInvalidMessage
	}
	queries := dbgen.New(tx)
	if err := queries.LockAccountMailState(ctx, account); err != nil {
		return fmt.Errorf("lock remote mail page: %w", err)
	}
	now := time.Now().UTC()
	for _, mailbox := range catalog.Mailboxes {
		id, idErr := newDatabaseID()
		if idErr != nil {
			return idErr
		}
		_, err = queries.ReconcileMailbox(ctx, dbgen.ReconcileMailboxParams{
			ID: id, AccountID: accountID, UserID: userID, RemoteID: mailbox.RemoteID,
			RemoteName: mailbox.Name, Role: optionalText(string(mailbox.Role)), Selectable: mailbox.Selectable,
			TotalCount: mailbox.TotalCount, UnreadCount: mailbox.UnreadCount, LastSyncedAt: optionalTime(&now),
		})
		if err != nil {
			return fmt.Errorf("reconcile remote mailbox: %w", err)
		}
	}
	for _, label := range catalog.Labels {
		id, idErr := newDatabaseID()
		if idErr != nil {
			return idErr
		}
		if label.Category != nil {
			_, err = queries.EnsureCategoryLabel(ctx, dbgen.EnsureCategoryLabelParams{
				ID: id, AccountID: accountID, UserID: userID, RemoteName: label.Name,
				Category: optionalText(string(*label.Category)),
			})
		} else {
			_, err = queries.ReconcileProviderLabel(ctx, dbgen.ReconcileProviderLabelParams{
				ID: id, AccountID: accountID, UserID: userID, RemoteID: optionalText(label.RemoteID),
				RemoteName: label.Name, Kind: string(label.Kind), TotalCount: label.TotalCount,
				UnreadCount: label.UnreadCount, LastSyncedAt: optionalTime(&now),
			})
		}
		if err != nil {
			return fmt.Errorf("reconcile remote label: %w", err)
		}
	}
	touchedThreads := make(map[string]struct{})
	messages := append([]RemoteMessage(nil), page.Messages...)
	sort.SliceStable(messages, func(left, right int) bool { return messages[left].SentAt.Before(messages[right].SentAt) })
	for _, remote := range messages {
		if !validRemoteMessage(remote) {
			return ErrInvalidMessage
		}
		deletedAt := (*time.Time)(nil)
		if remote.InTrash {
			deletedAt = &now
		}
		threadID, idErr := newDatabaseID()
		if idErr != nil {
			return idErr
		}
		thread, err := queries.UpsertThread(ctx, dbgen.UpsertThreadParams{
			ID: threadID, AccountID: accountID, UserID: userID, RemoteID: strings.TrimSpace(remote.ThreadID),
			LastMessageAt: requiredTime(remote.SentAt), IsRead: remote.IsRead, IsStarred: remote.IsStarred,
			IsImportant: remote.IsImportant, Category: string(remote.Category), DeletedAt: optionalTime(deletedAt),
		})
		if err != nil {
			return fmt.Errorf("upsert remote thread: %w", err)
		}
		messageID, idErr := newDatabaseID()
		if idErr != nil {
			return idErr
		}
		content := remote.Content
		message, err := queries.UpsertMessage(ctx, dbgen.UpsertMessageParams{
			ID: messageID, ThreadID: thread.ID, AccountID: accountID, UserID: userID,
			RemoteID: strings.TrimSpace(remote.RemoteID), MessageID: optionalText(content.MessageID),
			ReferencesHeader: append([]string{}, content.References...), InReplyTo: append([]string{}, content.InReplyTo...),
			Subject: content.Subject, BodyText: content.BodyText, BodyHtmlSanitized: content.BodyHTML,
			SentAt: requiredTime(remote.SentAt), IsRead: remote.IsRead, IsStarred: remote.IsStarred,
			IsImportant: remote.IsImportant, DeletedAt: optionalTime(deletedAt),
		})
		if err != nil {
			return fmt.Errorf("upsert remote message: %w", err)
		}
		if err := queries.DeleteMessageAddresses(ctx, dbgen.DeleteMessageAddressesParams{MessageID: message.ID, AccountID: accountID}); err != nil {
			return fmt.Errorf("replace remote addresses: %w", err)
		}
		for _, address := range normalizeAddresses(content.Addresses) {
			if err := queries.CreateMessageAddress(ctx, dbgen.CreateMessageAddressParams{
				MessageID: message.ID, AccountID: accountID, Role: string(address.Role), Position: address.Position,
				DisplayName: optionalText(pointerValue(address.DisplayName)), Address: address.Address,
			}); err != nil {
				return fmt.Errorf("create remote address: %w", err)
			}
		}
		if err := queries.DeleteMessageAttachmentsFromPosition(ctx, dbgen.DeleteMessageAttachmentsFromPositionParams{MessageID: message.ID, AccountID: accountID, FromPosition: int32(len(content.Attachments))}); err != nil {
			return fmt.Errorf("trim remote attachments: %w", err)
		}
		for position, attachment := range content.Attachments {
			attachmentID, idErr := newDatabaseID()
			if idErr != nil {
				return idErr
			}
			if _, err := queries.UpsertMessageAttachment(ctx, dbgen.UpsertMessageAttachmentParams{
				ID: attachmentID, MessageID: message.ID, AccountID: accountID, Position: int32(position),
				RemoteID: optionalText(attachment.RemoteID), Filename: optionalText(attachment.Filename),
				MediaType: strings.ToLower(strings.TrimSpace(attachment.MediaType)), Disposition: attachment.Disposition,
				ContentID: optionalText(attachment.ContentID), SizeBytes: attachment.SizeBytes,
			}); err != nil {
				return fmt.Errorf("upsert remote attachment: %w", err)
			}
		}
		touchedThreads[threadUUID(thread.ID)] = struct{}{}
	}
	if len(page.DeletedRemoteIDs) > 0 {
		threadIDs, err := queries.SoftDeleteRemoteMessages(ctx, dbgen.SoftDeleteRemoteMessagesParams{AccountID: accountID, UserID: userID, RemoteIds: page.DeletedRemoteIDs})
		if err != nil {
			return fmt.Errorf("delete remote messages: %w", err)
		}
		for _, threadID := range threadIDs {
			touchedThreads[threadUUID(threadID)] = struct{}{}
		}
	}
	for value := range touchedThreads {
		threadID, parseErr := databaseID(value)
		if parseErr != nil {
			return ErrInvalidThread
		}
		if _, err := queries.RefreshThreadSummary(ctx, dbgen.RefreshThreadSummaryParams{ThreadID: threadID, AccountID: accountID}); err != nil {
			return fmt.Errorf("refresh remote thread: %w", err)
		}
	}
	return nil
}

func validRemoteMessage(remote RemoteMessage) bool {
	input := UpsertMessageInput{
		RemoteID: remote.RemoteID, ThreadID: remote.ThreadID, SentAt: remote.SentAt,
		MessageID: remote.Content.MessageID, References: remote.Content.References, InReplyTo: remote.Content.InReplyTo,
		Subject: remote.Content.Subject, BodyText: remote.Content.BodyText, BodyHTML: remote.Content.BodyHTML,
		Addresses: remote.Content.Addresses, Attachments: remote.Content.Attachments,
	}
	return boundedText(remote.ThreadID, 512) && validCategory(remote.Category) && validMessageInput(input)
}

func threadUUID(value pgtype.UUID) string { return uuid.UUID(value.Bytes).String() }
