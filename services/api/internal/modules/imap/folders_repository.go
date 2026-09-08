package imap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FolderRepositoryStore struct{ pool *pgxpool.Pool }

func NewFolderRepository(pool *pgxpool.Pool) *FolderRepositoryStore {
	return &FolderRepositoryStore{pool: pool}
}

func (repository *FolderRepositoryStore) Reconcile(ctx context.Context, user, account string, folders []DiscoveredFolder) (FolderDiscoveryResult, error) {
	userID, accountID, err := folderOwnerIDs(user, account)
	if err != nil || len(folders) > maxDiscoveredFolders {
		return FolderDiscoveryResult{}, ErrFolderPersistence
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return FolderDiscoveryResult{}, fmt.Errorf("%w: begin transaction", ErrFolderPersistence)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	existingRows, err := queries.ListIMAPFolderStatesByOwner(ctx, dbgen.ListIMAPFolderStatesByOwnerParams{AccountID: accountID, UserID: userID})
	if err != nil {
		return FolderDiscoveryResult{}, fmt.Errorf("%w: list current folders", ErrFolderPersistence)
	}
	present := make(map[string]bool, len(folders))
	for _, folder := range folders {
		present[folder.IdentityKey] = true
	}
	existing := make(map[string]dbgen.ListIMAPFolderStatesByOwnerRow, len(existingRows))
	for _, row := range existingRows {
		existing[row.IdentityKey] = row
	}
	used := make(map[string]bool, len(folders))
	result := FolderDiscoveryResult{Folders: make([]FolderState, 0, len(folders))}
	now := time.Now().UTC()
	for _, folder := range folders {
		current, found := existing[folder.IdentityKey]
		if !found {
			current, found, err = renameCandidate(existingRows, present, used, folder)
			if err != nil {
				return FolderDiscoveryResult{}, err
			}
		}
		mailboxID, err := repository.reconcileMailbox(ctx, queries, userID, accountID, current, found, folder, now)
		if err != nil {
			return FolderDiscoveryResult{}, err
		}
		if found {
			used[current.IdentityKey] = true
		}
		nextUID := int64(1)
		state := FolderCursorActive
		if !folder.Selectable {
			state = FolderCursorNotSelectable
		} else if found && current.UidValidity.Valid && folder.UIDValidity != nil && current.UidValidity.Int64 == *folder.UIDValidity && current.NextUid.Valid {
			nextUID = current.NextUid.Int64
		}
		cursor, err := queries.UpsertIMAPFolderState(ctx, dbgen.UpsertIMAPFolderStateParams{
			MailboxID: mailboxID, AccountID: accountID, IdentityKey: folder.IdentityKey,
			NamespacePrefix: folder.NamespacePrefix, Delimiter: optionalFolderText(folder.Delimiter),
			Subscribed: folder.Subscribed, UidNext: optionalFolderInt(folder.UIDNext),
			UidValidity: optionalFolderInt(folder.UIDValidity), NextUid: selectableNextUID(folder.Selectable, nextUID), State: string(state),
		})
		if err != nil {
			return FolderDiscoveryResult{}, fmt.Errorf("%w: store folder cursor", ErrFolderPersistence)
		}
		folderState := mapFolderState(mailboxID, folder, cursor)
		result.Folders = append(result.Folders, folderState)
		result.ReconciliationRequired = result.ReconciliationRequired || folderState.CursorState == FolderCursorResyncRequired
	}
	for _, row := range existingRows {
		if present[row.IdentityKey] || used[row.IdentityKey] {
			continue
		}
		missing := dbgen.MarkIMAPFolderMissingParams{UserID: userID, AccountID: accountID, IdentityKey: row.IdentityKey}
		if err := queries.MarkIMAPFolderMissing(ctx, missing); err != nil {
			return FolderDiscoveryResult{}, fmt.Errorf("%w: mark missing mailbox", ErrFolderPersistence)
		}
		if err := queries.MarkIMAPFolderCursorMissing(ctx, dbgen.MarkIMAPFolderCursorMissingParams(missing)); err != nil {
			return FolderDiscoveryResult{}, fmt.Errorf("%w: mark missing cursor", ErrFolderPersistence)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return FolderDiscoveryResult{}, fmt.Errorf("%w: commit reconciliation", ErrFolderPersistence)
	}
	return result, nil
}

func (repository *FolderRepositoryStore) reconcileMailbox(ctx context.Context, queries *dbgen.Queries, userID, accountID pgtype.UUID, current dbgen.ListIMAPFolderStatesByOwnerRow, found bool, folder DiscoveredFolder, now time.Time) (pgtype.UUID, error) {
	role := optionalFolderText(string(folder.Role))
	revision := optionalFolderText(folderRevision(folder))
	if found {
		_, err := queries.RenameIMAPMailboxIdentity(ctx, dbgen.RenameIMAPMailboxIdentityParams{
			RemoteID: folder.IdentityKey, RemoteName: folder.Name, Role: role, Selectable: folder.Selectable,
			TotalCount: folder.TotalCount, UnreadCount: folder.UnreadCount, RemoteRevision: revision,
			MailboxID: current.MailboxID, AccountID: accountID, UserID: userID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, ErrFolderPersistence
		}
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("%w: update mailbox", ErrFolderPersistence)
		}
		return current.MailboxID, nil
	}
	id, err := ids.New()
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: create mailbox ID", ErrFolderPersistence)
	}
	mailboxUUID, _ := uuid.Parse(id)
	row, err := queries.ReconcileMailbox(ctx, dbgen.ReconcileMailboxParams{
		ID: pgtype.UUID{Bytes: mailboxUUID, Valid: true}, RemoteID: folder.IdentityKey, RemoteName: folder.Name,
		Role: role, Selectable: folder.Selectable, TotalCount: folder.TotalCount, UnreadCount: folder.UnreadCount,
		RemoteRevision: revision, LastSyncedAt: pgtype.Timestamptz{Time: now, Valid: true}, AccountID: accountID, UserID: userID,
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: create mailbox", ErrFolderPersistence)
	}
	return row.ID, nil
}

func renameCandidate(existing []dbgen.ListIMAPFolderStatesByOwnerRow, present, used map[string]bool, folder DiscoveredFolder) (dbgen.ListIMAPFolderStatesByOwnerRow, bool, error) {
	if folder.Role != "" || folder.UIDValidity == nil {
		return dbgen.ListIMAPFolderStatesByOwnerRow{}, false, nil
	}
	var candidates []dbgen.ListIMAPFolderStatesByOwnerRow
	for _, row := range existing {
		if present[row.IdentityKey] || used[row.IdentityKey] || row.Role.Valid || !row.UidValidity.Valid || row.UidValidity.Int64 != *folder.UIDValidity {
			continue
		}
		candidates = append(candidates, row)
	}
	if len(candidates) > 1 {
		return dbgen.ListIMAPFolderStatesByOwnerRow{}, false, ErrFolderIdentityConflict
	}
	if len(candidates) == 1 {
		return candidates[0], true, nil
	}
	return dbgen.ListIMAPFolderStatesByOwnerRow{}, false, nil
}

func mapFolderState(mailboxID pgtype.UUID, folder DiscoveredFolder, cursor dbgen.ImapFolderCursor) FolderState {
	return FolderState{
		MailboxID: uuid.UUID(mailboxID.Bytes).String(), RemoteID: folder.IdentityKey, Name: folder.Name,
		Role: folder.Role, Selectable: folder.Selectable, Subscribed: folder.Subscribed,
		NamespacePrefix: folder.NamespacePrefix, Delimiter: folderTextPointer(cursor.Delimiter),
		UIDNext: folderIntPointer(cursor.UidNext), UIDValidity: folderIntPointer(cursor.UidValidity), NextUID: folderIntPointer(cursor.NextUid),
		CursorState: FolderCursorState(cursor.State), CursorVersion: cursor.Version,
		InvalidatedAt: folderTimePointer(cursor.InvalidatedAt), InvalidationReason: folderTextPointer(cursor.InvalidationReason),
	}
}

func folderRevision(folder DiscoveredFolder) string {
	return fmt.Sprintf("uidvalidity=%d;uidnext=%d;attributes=%s", pointerValue(folder.UIDValidity), pointerValue(folder.UIDNext), strings.Join(folder.Attributes, ","))
}

func folderOwnerIDs(user, account string) (pgtype.UUID, pgtype.UUID, error) {
	userUUID, err := uuid.Parse(user)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	accountUUID, err := uuid.Parse(account)
	return pgtype.UUID{Bytes: userUUID, Valid: true}, pgtype.UUID{Bytes: accountUUID, Valid: err == nil}, err
}

func optionalFolderText(value string) pgtype.Text {
	value = strings.TrimSpace(value)
	return pgtype.Text{String: value, Valid: value != ""}
}

func optionalFolderInt(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}

func selectableNextUID(selectable bool, value int64) pgtype.Int8 {
	if !selectable {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: value, Valid: true}
}

func pointerValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func folderIntPointer(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func folderTextPointer(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func folderTimePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

var _ FolderRepository = (*FolderRepositoryStore)(nil)
