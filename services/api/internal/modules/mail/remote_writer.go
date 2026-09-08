package mail

import (
	"context"
	"errors"
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
	return writer.ApplyRemotePage(ctx, tx, user, account, catalog, page)
}

func (writer *RemotePageWriter) ApplyRemotePage(ctx context.Context, tx pgx.Tx, user, account string, catalog CatalogPage, page ChangePage) error {
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
		for _, location := range remote.Locations {
			if !validRemoteLocation(location) {
				return ErrInvalidMessage
			}
			_, linkErr := queries.LinkMessageMailboxByRemoteID(ctx, dbgen.LinkMessageMailboxByRemoteIDParams{
				MessageID: message.ID, AccountID: accountID, UserID: userID, MailboxRemoteID: location.MailboxID,
			})
			if linkErr != nil {
				return fmt.Errorf("link remote message mailbox: %w", linkErr)
			}
			if _, locationErr := queries.UpsertIMAPMessageLocation(ctx, dbgen.UpsertIMAPMessageLocationParams{
				MessageID: message.ID, AccountID: accountID, UserID: userID, MailboxRemoteID: location.MailboxID,
				UidValidity: location.UIDValidity, Uid: location.UID,
			}); locationErr != nil {
				return fmt.Errorf("upsert IMAP message location: %w", locationErr)
			}
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
	for _, snapshot := range page.LocationSnapshots {
		if !validRemoteLocationSnapshot(snapshot) {
			return ErrInvalidMessage
		}
		rows, snapshotErr := tx.Query(ctx, `delete from imap_message_locations
using mailboxes, accounts
where imap_message_locations.mailbox_id = mailboxes.id
  and imap_message_locations.account_id = mailboxes.account_id
  and accounts.id = imap_message_locations.account_id
  and accounts.user_id = $1
  and imap_message_locations.account_id = $2
  and mailboxes.remote_id = $3
  and (imap_message_locations.uid_validity <> $4 or not (imap_message_locations.uid = any($5::bigint[])))
returning imap_message_locations.message_id, imap_message_locations.mailbox_id`, userID, accountID, snapshot.MailboxID, snapshot.UIDValidity, snapshot.PresentUIDs)
		if snapshotErr != nil {
			return fmt.Errorf("reconcile IMAP location snapshot: %w", snapshotErr)
		}
		type removedLocation struct{ messageID, mailboxID pgtype.UUID }
		var removed []removedLocation
		for rows.Next() {
			var item removedLocation
			if err := rows.Scan(&item.messageID, &item.mailboxID); err != nil {
				rows.Close()
				return fmt.Errorf("scan removed IMAP location: %w", err)
			}
			removed = append(removed, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("read removed IMAP locations: %w", err)
		}
		rows.Close()
		for _, item := range removed {
			if _, err := tx.Exec(ctx, `delete from message_mailboxes where message_id = $1 and mailbox_id = $2 and account_id = $3`, item.messageID, item.mailboxID, accountID); err != nil {
				return fmt.Errorf("unlink stale IMAP mailbox: %w", err)
			}
			var threadID pgtype.UUID
			err := tx.QueryRow(ctx, `update messages set deleted_at = $1, updated_at = $1
where id = $2 and account_id = $3
  and not exists (select 1 from imap_message_locations where message_id = messages.id and account_id = messages.account_id)
returning thread_id`, now, item.messageID, accountID).Scan(&threadID)
			if err == nil {
				touchedThreads[threadUUID(threadID)] = struct{}{}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("delete orphaned IMAP message: %w", err)
			}
		}
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

func validRemoteLocation(location RemoteLocation) bool {
	return boundedText(location.MailboxID, 512) && location.UIDValidity > 0 && location.UID > 0
}

func validRemoteLocationSnapshot(snapshot RemoteLocationSnapshot) bool {
	if !boundedText(snapshot.MailboxID, 512) || snapshot.UIDValidity < 1 || len(snapshot.PresentUIDs) > 1_000_000 {
		return false
	}
	previous := int64(0)
	for _, uid := range snapshot.PresentUIDs {
		if uid <= previous {
			return false
		}
		previous = uid
	}
	return true
}

func threadUUID(value pgtype.UUID) string { return uuid.UUID(value.Bytes).String() }
