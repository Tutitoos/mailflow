package imap

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MessageLocation struct {
	MessageRemoteID string
	MailboxRemoteID string
	WireName        string
	Role            mail.MailboxRole
	UIDValidity     int64
	UID             int64
}

type ProviderStore interface {
	Folders(context.Context, string, string) ([]FolderState, error)
	Locations(context.Context, string, string, []string) ([]MessageLocation, error)
	MoveLocation(context.Context, string, string, MessageLocation, MessageLocation) error
}

type DatabaseProviderStore struct{ pool *pgxpool.Pool }

func NewDatabaseProviderStore(pool *pgxpool.Pool) (*DatabaseProviderStore, error) {
	if pool == nil {
		return nil, ErrInvalidConfiguration
	}
	return &DatabaseProviderStore{pool: pool}, nil
}

func (store *DatabaseProviderStore) Folders(ctx context.Context, user, account string) ([]FolderState, error) {
	userID, accountID, err := folderOwnerIDs(user, account)
	if err != nil {
		return nil, ErrFolderPersistence
	}
	rows, err := dbgen.New(store.pool).ListIMAPFolderStatesByOwner(ctx, dbgen.ListIMAPFolderStatesByOwnerParams{AccountID: accountID, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("%w: list provider folders", ErrFolderPersistence)
	}
	result := make([]FolderState, 0, len(rows))
	for _, row := range rows {
		result = append(result, FolderState{
			MailboxID: uuid.UUID(row.MailboxID.Bytes).String(), RemoteID: row.RemoteID,
			WireName: row.WireName, Name: row.RemoteName, Role: mail.MailboxRole(row.Role.String),
			Selectable: row.Selectable, Subscribed: row.Subscribed, NamespacePrefix: row.NamespacePrefix,
			Delimiter: folderTextPointer(row.Delimiter), UIDNext: folderIntPointer(row.UidNext),
			UIDValidity: folderIntPointer(row.UidValidity), NextUID: folderIntPointer(row.NextUid),
			CursorState: FolderCursorState(row.State), CursorVersion: row.Version,
			InvalidatedAt: folderTimePointer(row.InvalidatedAt), InvalidationReason: folderTextPointer(row.InvalidationReason),
		})
	}
	return result, nil
}

func (store *DatabaseProviderStore) Locations(ctx context.Context, user, account string, remoteIDs []string) ([]MessageLocation, error) {
	userID, accountID, err := folderOwnerIDs(user, account)
	if err != nil || len(remoteIDs) == 0 || len(remoteIDs) > 1000 {
		return nil, ErrInvalidMessageLocation
	}
	rows, err := dbgen.New(store.pool).ListIMAPMessageLocations(ctx, dbgen.ListIMAPMessageLocationsParams{AccountID: accountID, UserID: userID, MessageRemoteIds: remoteIDs})
	if err != nil {
		return nil, fmt.Errorf("%w: list message locations", ErrMessagePersistence)
	}
	result := make([]MessageLocation, 0, len(rows))
	for _, row := range rows {
		result = append(result, MessageLocation{
			MessageRemoteID: row.MessageRemoteID, MailboxRemoteID: row.MailboxRemoteID,
			WireName: row.WireName, Role: mail.MailboxRole(row.Role.String), UIDValidity: row.UidValidity, UID: row.Uid,
		})
	}
	return result, nil
}

func (store *DatabaseProviderStore) MoveLocation(ctx context.Context, user, account string, source, destination MessageLocation) error {
	userID, accountID, err := folderOwnerIDs(user, account)
	if err != nil || source.MessageRemoteID == "" || destination.MailboxRemoteID == "" || destination.UIDValidity < 1 || destination.UID < 1 {
		return ErrInvalidMessageLocation
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: begin location move", ErrMessagePersistence)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var messageID pgtype.UUID
	err = tx.QueryRow(ctx, `select messages.id from messages join accounts on accounts.id = messages.account_id
where messages.account_id = $1 and accounts.user_id = $2 and accounts.provider = 'imap'
and accounts.disabled_at is null and messages.remote_id = $3`, accountID, userID, source.MessageRemoteID).Scan(&messageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidMessageLocation
	}
	if err != nil {
		return fmt.Errorf("%w: resolve moved message", ErrMessagePersistence)
	}
	queries := dbgen.New(tx)
	removed, err := queries.DeleteIMAPMessageLocation(ctx, dbgen.DeleteIMAPMessageLocationParams{
		UserID: userID, AccountID: accountID, MessageRemoteID: source.MessageRemoteID,
		MailboxRemoteID: source.MailboxRemoteID, UidValidity: source.UIDValidity, Uid: source.UID,
	})
	if err != nil || removed != 1 {
		return ErrInvalidMessageLocation
	}
	if _, err := queries.UpsertIMAPMessageLocation(ctx, dbgen.UpsertIMAPMessageLocationParams{
		MessageID: messageID, AccountID: accountID, UserID: userID,
		MailboxRemoteID: destination.MailboxRemoteID, UidValidity: destination.UIDValidity, Uid: destination.UID,
	}); err != nil {
		return fmt.Errorf("%w: store moved message", ErrMessagePersistence)
	}
	if _, err := queries.LinkMessageMailboxByRemoteID(ctx, dbgen.LinkMessageMailboxByRemoteIDParams{
		MessageID: messageID, AccountID: accountID, UserID: userID, MailboxRemoteID: destination.MailboxRemoteID,
	}); err != nil {
		return fmt.Errorf("%w: link moved message", ErrMessagePersistence)
	}
	if _, err := tx.Exec(ctx, `delete from message_mailboxes using mailboxes
where message_mailboxes.message_id = $1 and message_mailboxes.account_id = $2
and mailboxes.id = message_mailboxes.mailbox_id and mailboxes.account_id = message_mailboxes.account_id
and mailboxes.remote_id = $3`, messageID, accountID, source.MailboxRemoteID); err != nil {
		return fmt.Errorf("%w: unlink moved message", ErrMessagePersistence)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit location move", ErrMessagePersistence)
	}
	return nil
}

var _ ProviderStore = (*DatabaseProviderStore)(nil)
