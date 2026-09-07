package mail

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
)

type MailboxLabelRepositoryStore struct {
	queries *dbgen.Queries
}

func NewMailboxLabelRepository(queries *dbgen.Queries) *MailboxLabelRepositoryStore {
	return &MailboxLabelRepositoryStore{queries: queries}
}

func (repository *MailboxLabelRepositoryStore) ReconcileMailbox(ctx context.Context, input ReconcileMailboxInput) (Mailbox, error) {
	userID, accountID, err := ownerAccountIDs(input.UserID, input.AccountID)
	if err != nil || !validMailboxInput(input) {
		return Mailbox{}, ErrInvalidMailbox
	}
	id, err := newDatabaseID()
	if err != nil {
		return Mailbox{}, err
	}
	row, err := repository.queries.ReconcileMailbox(ctx, dbgen.ReconcileMailboxParams{
		ID: id, RemoteID: strings.TrimSpace(input.RemoteID), RemoteName: strings.TrimSpace(input.RemoteName),
		Role: optionalText(string(input.Role)), Selectable: input.Selectable,
		TotalCount: input.TotalCount, UnreadCount: input.UnreadCount,
		RemoteRevision: optionalText(input.RemoteRevision), LastSyncedAt: optionalTime(input.LastSyncedAt),
		AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Mailbox{}, ErrMailboxNotFound
	}
	if err != nil {
		return Mailbox{}, fmt.Errorf("reconcile mailbox: %w", err)
	}
	return mapMailbox(row), nil
}

func (repository *MailboxLabelRepositoryStore) ListMailboxes(ctx context.Context, user, account string) ([]Mailbox, error) {
	userID, accountID, err := ownerAccountIDs(user, account)
	if err != nil {
		return nil, ErrInvalidMailbox
	}
	rows, err := repository.queries.ListMailboxesByAccount(ctx, dbgen.ListMailboxesByAccountParams{AccountID: accountID, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("list mailboxes: %w", err)
	}
	result := make([]Mailbox, 0, len(rows))
	for _, row := range rows {
		result = append(result, mapMailbox(row))
	}
	return result, nil
}

func (repository *MailboxLabelRepositoryStore) RenameMailbox(ctx context.Context, user, account, mailbox, localName string) (Mailbox, error) {
	userID, accountID, mailboxID, err := scopedResourceIDs(user, account, mailbox)
	if err != nil || len(strings.TrimSpace(localName)) > 256 {
		return Mailbox{}, ErrInvalidMailbox
	}
	row, err := repository.queries.RenameMailboxLocal(ctx, dbgen.RenameMailboxLocalParams{LocalName: localName, ID: mailboxID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Mailbox{}, ErrMailboxNotFound
	}
	if err != nil {
		return Mailbox{}, fmt.Errorf("rename mailbox: %w", err)
	}
	return mapMailbox(row), nil
}

func (repository *MailboxLabelRepositoryStore) UpdateMailboxCounters(ctx context.Context, user, account, mailbox string, total, unread int32) (Mailbox, error) {
	userID, accountID, mailboxID, err := scopedResourceIDs(user, account, mailbox)
	if err != nil || !validCounts(total, unread) {
		return Mailbox{}, ErrInvalidMailbox
	}
	row, err := repository.queries.UpdateMailboxCounters(ctx, dbgen.UpdateMailboxCountersParams{TotalCount: total, UnreadCount: unread, ID: mailboxID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Mailbox{}, ErrMailboxNotFound
	}
	if err != nil {
		return Mailbox{}, fmt.Errorf("update mailbox counters: %w", err)
	}
	return mapMailbox(row), nil
}

func (repository *MailboxLabelRepositoryStore) ReconcileProviderLabel(ctx context.Context, input ReconcileProviderLabelInput) (Label, error) {
	userID, accountID, err := ownerAccountIDs(input.UserID, input.AccountID)
	if err != nil || !validProviderLabelInput(input) {
		return Label{}, ErrInvalidLabel
	}
	id, err := newDatabaseID()
	if err != nil {
		return Label{}, err
	}
	row, err := repository.queries.ReconcileProviderLabel(ctx, dbgen.ReconcileProviderLabelParams{
		ID: id, RemoteID: optionalText(input.RemoteID), RemoteName: strings.TrimSpace(input.RemoteName),
		Kind: string(input.Kind), Color: optionalText(input.Color), TotalCount: input.TotalCount,
		UnreadCount: input.UnreadCount, RemoteRevision: optionalText(input.RemoteRevision),
		LastSyncedAt: optionalTime(input.LastSyncedAt), AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Label{}, ErrLabelNotFound
	}
	if err != nil {
		return Label{}, fmt.Errorf("reconcile provider label: %w", err)
	}
	return mapLabel(row), nil
}

func (repository *MailboxLabelRepositoryStore) EnsureCategoryLabel(ctx context.Context, input EnsureCategoryLabelInput) (Label, error) {
	userID, accountID, err := ownerAccountIDs(input.UserID, input.AccountID)
	if err != nil || !validCategory(input.Category) || !boundedText(input.Name, 256) || len(strings.TrimSpace(input.Color)) > 64 {
		return Label{}, ErrInvalidLabel
	}
	id, err := newDatabaseID()
	if err != nil {
		return Label{}, err
	}
	row, err := repository.queries.EnsureCategoryLabel(ctx, dbgen.EnsureCategoryLabelParams{
		ID: id, RemoteName: strings.TrimSpace(input.Name), Category: optionalText(string(input.Category)),
		Color: optionalText(input.Color), AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Label{}, ErrLabelNotFound
	}
	if err != nil {
		return Label{}, fmt.Errorf("ensure category label: %w", err)
	}
	return mapLabel(row), nil
}

func (repository *MailboxLabelRepositoryStore) ListLabels(ctx context.Context, user, account string) ([]Label, error) {
	userID, accountID, err := ownerAccountIDs(user, account)
	if err != nil {
		return nil, ErrInvalidLabel
	}
	rows, err := repository.queries.ListLabelsByAccount(ctx, dbgen.ListLabelsByAccountParams{AccountID: accountID, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	result := make([]Label, 0, len(rows))
	for _, row := range rows {
		result = append(result, mapLabel(row))
	}
	return result, nil
}

func (repository *MailboxLabelRepositoryStore) RenameLabel(ctx context.Context, user, account, label, localName string) (Label, error) {
	userID, accountID, labelID, err := scopedResourceIDs(user, account, label)
	if err != nil || len(strings.TrimSpace(localName)) > 256 {
		return Label{}, ErrInvalidLabel
	}
	row, err := repository.queries.RenameLabelLocal(ctx, dbgen.RenameLabelLocalParams{LocalName: localName, ID: labelID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Label{}, ErrLabelNotFound
	}
	if err != nil {
		return Label{}, fmt.Errorf("rename label: %w", err)
	}
	return mapLabel(row), nil
}

func (repository *MailboxLabelRepositoryStore) UpdateLabelCounters(ctx context.Context, user, account, label string, total, unread int32) (Label, error) {
	userID, accountID, labelID, err := scopedResourceIDs(user, account, label)
	if err != nil || !validCounts(total, unread) {
		return Label{}, ErrInvalidLabel
	}
	row, err := repository.queries.UpdateLabelCounters(ctx, dbgen.UpdateLabelCountersParams{TotalCount: total, UnreadCount: unread, ID: labelID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Label{}, ErrLabelNotFound
	}
	if err != nil {
		return Label{}, fmt.Errorf("update label counters: %w", err)
	}
	return mapLabel(row), nil
}

func validMailboxInput(input ReconcileMailboxInput) bool {
	return boundedText(input.RemoteID, 512) && boundedText(input.RemoteName, 256) && validMailboxRole(input.Role) && validCounts(input.TotalCount, input.UnreadCount) && len(strings.TrimSpace(input.RemoteRevision)) <= 512
}

func validProviderLabelInput(input ReconcileProviderLabelInput) bool {
	return boundedText(input.RemoteID, 512) && boundedText(input.RemoteName, 256) && (input.Kind == LabelSystem || input.Kind == LabelUser) && validCounts(input.TotalCount, input.UnreadCount) && len(strings.TrimSpace(input.Color)) <= 64 && len(strings.TrimSpace(input.RemoteRevision)) <= 512
}

func validMailboxRole(role MailboxRole) bool {
	switch role {
	case "", MailboxInbox, MailboxSent, MailboxDrafts, MailboxTrash, MailboxJunk, MailboxArchive, MailboxAll:
		return true
	default:
		return false
	}
}

func validCategory(category Category) bool {
	switch category {
	case CategoryPrimary, CategoryPromotions, CategorySocial, CategoryNotifications, CategoryForums:
		return true
	default:
		return false
	}
}

func validCounts(total, unread int32) bool { return total >= 0 && unread >= 0 && unread <= total }

func boundedText(value string, limit int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= limit
}

func ownerAccountIDs(user, account string) (pgtype.UUID, pgtype.UUID, error) {
	userID, err := databaseID(user)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	accountID, err := databaseID(account)
	return userID, accountID, err
}

func scopedResourceIDs(user, account, resource string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	userID, accountID, err := ownerAccountIDs(user, account)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, err
	}
	resourceID, err := databaseID(resource)
	return userID, accountID, resourceID, err
}

func databaseID(value string) (pgtype.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func newDatabaseID() (pgtype.UUID, error) {
	value, err := ids.New()
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create resource ID: %w", err)
	}
	return databaseID(value)
}

func optionalText(value string) pgtype.Text {
	trimmed := strings.TrimSpace(value)
	return pgtype.Text{String: trimmed, Valid: trimmed != ""}
}

func optionalTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func mapMailbox(row dbgen.Mailbox) Mailbox {
	return Mailbox{
		ID: uuid.UUID(row.ID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(),
		RemoteID: row.RemoteID, RemoteName: row.RemoteName, LocalName: textPointer(row.LocalName),
		Role: MailboxRole(row.Role.String), Selectable: row.Selectable,
		TotalCount: row.TotalCount, UnreadCount: row.UnreadCount,
		RemoteRevision: textPointer(row.RemoteRevision), LastSyncedAt: timePointer(row.LastSyncedAt),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}

func mapLabel(row dbgen.Label) Label {
	var category *Category
	if row.Category.Valid {
		value := Category(row.Category.String)
		category = &value
	}
	return Label{
		ID: uuid.UUID(row.ID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(),
		RemoteID: textPointer(row.RemoteID), RemoteName: row.RemoteName, LocalName: textPointer(row.LocalName),
		Kind: LabelKind(row.Kind), Category: category, Color: textPointer(row.Color),
		TotalCount: row.TotalCount, UnreadCount: row.UnreadCount,
		RemoteRevision: textPointer(row.RemoteRevision), LastSyncedAt: timePointer(row.LastSyncedAt),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}

func textPointer(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}
