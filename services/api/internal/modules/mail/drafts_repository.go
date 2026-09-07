package mail

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var draftObjectIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type DraftRepositoryStore struct{ pool *pgxpool.Pool }

func NewDraftRepository(pool *pgxpool.Pool) *DraftRepositoryStore {
	return &DraftRepositoryStore{pool: pool}
}

func (repository *DraftRepositoryStore) CreateDraft(ctx context.Context, input CreateDraftInput) (Draft, error) {
	userID, accountID, err := ownerAccountIDs(input.UserID, input.AccountID)
	content, err := prepareDraftContent(input.Content, err)
	if err != nil || input.Now.IsZero() {
		return Draft{}, ErrInvalidDraft
	}
	id, err := newDatabaseID()
	if err != nil {
		return Draft{}, err
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Draft{}, fmt.Errorf("begin draft creation: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	row, err := queries.CreateDraft(ctx, dbgen.CreateDraftParams{
		ID: id, Subject: content.Subject, BodyText: content.BodyText, BodyHtmlSanitized: content.BodyHTML,
		RemoteCheckpointAt: requiredTime(input.Now.UTC().Add(DraftRemoteInterval)), AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrDraftNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("create draft: %w", err)
	}
	if err := replaceDraftRelations(ctx, queries, row.ID, row.AccountID, content); err != nil {
		return Draft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Draft{}, fmt.Errorf("commit draft creation: %w", err)
	}
	return repository.hydrate(ctx, row)
}

func (repository *DraftRepositoryStore) GetDraft(ctx context.Context, user, account, draft string) (Draft, error) {
	userID, accountID, draftID, err := scopedResourceIDs(user, account, draft)
	if err != nil {
		return Draft{}, ErrInvalidDraft
	}
	row, err := dbgen.New(repository.pool).GetDraftByOwner(ctx, dbgen.GetDraftByOwnerParams{ID: draftID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrDraftNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("get draft: %w", err)
	}
	return repository.hydrate(ctx, row)
}

func (repository *DraftRepositoryStore) UpdateDraft(ctx context.Context, input UpdateDraftInput) (Draft, error) {
	userID, accountID, draftID, err := scopedResourceIDs(input.UserID, input.AccountID, input.DraftID)
	content, err := prepareDraftContent(input.Content, err)
	if err != nil || input.ExpectedRevision < 1 || input.Now.IsZero() {
		return Draft{}, ErrInvalidDraft
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Draft{}, fmt.Errorf("begin draft update: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	row, err := queries.UpdateDraft(ctx, dbgen.UpdateDraftParams{
		Subject: content.Subject, BodyText: content.BodyText, BodyHtmlSanitized: content.BodyHTML,
		RemoteCheckpointAt: requiredTime(input.Now.UTC().Add(DraftRemoteInterval)), ID: draftID,
		AccountID: accountID, ExpectedRevision: input.ExpectedRevision, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, findErr := queries.GetDraftByOwner(ctx, dbgen.GetDraftByOwnerParams{ID: draftID, AccountID: accountID, UserID: userID}); findErr == nil {
			return Draft{}, ErrDraftConflict
		}
		return Draft{}, ErrDraftNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("update draft: %w", err)
	}
	if err := replaceDraftRelations(ctx, queries, row.ID, row.AccountID, content); err != nil {
		return Draft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Draft{}, fmt.Errorf("commit draft update: %w", err)
	}
	return repository.hydrate(ctx, row)
}

func (repository *DraftRepositoryStore) CheckpointRemote(ctx context.Context, input RemoteDraftCheckpoint) (Draft, error) {
	userID, accountID, draftID, err := scopedResourceIDs(input.UserID, input.AccountID, input.DraftID)
	if err != nil || input.LocalRevision < 1 || !boundedText(input.RemoteID, 512) || !boundedText(input.RemoteRevision, 512) {
		return Draft{}, ErrInvalidDraft
	}
	remoteID, remoteRevision := strings.TrimSpace(input.RemoteID), strings.TrimSpace(input.RemoteRevision)
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Draft{}, fmt.Errorf("begin draft checkpoint: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	queries := dbgen.New(tx)
	currentRow, err := queries.LockDraftByOwner(ctx, dbgen.LockDraftByOwnerParams{ID: draftID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrDraftNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("lock draft checkpoint: %w", err)
	}
	current := mapDraft(currentRow)
	if current.RemoteID != nil && current.RemoteRevision != nil && *current.RemoteID == remoteID && *current.RemoteRevision == remoteRevision && current.SyncedRevision >= input.LocalRevision {
		return repository.hydrate(ctx, currentRow)
	}
	if current.SyncStatus == DraftDiscarded || current.LocalRevision != input.LocalRevision || (current.RemoteID != nil && *current.RemoteID != remoteID) {
		return Draft{}, ErrDraftConflict
	}
	row, err := queries.CheckpointDraftRemote(ctx, dbgen.CheckpointDraftRemoteParams{
		RemoteID: optionalText(remoteID), RemoteRevision: optionalText(remoteRevision),
		SyncedRevision: input.LocalRevision, ID: draftID, AccountID: accountID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrDraftConflict
	}
	if err != nil {
		return Draft{}, fmt.Errorf("checkpoint remote draft: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Draft{}, fmt.Errorf("commit draft checkpoint: %w", err)
	}
	return repository.hydrate(ctx, row)
}

func (repository *DraftRepositoryStore) MarkDraftConflict(ctx context.Context, user, account, draft, remoteID, remoteRevision string) (Draft, error) {
	userID, accountID, draftID, err := scopedResourceIDs(user, account, draft)
	if err != nil || (remoteID != "" && !boundedText(remoteID, 512)) || (remoteRevision != "" && !boundedText(remoteRevision, 512)) {
		return Draft{}, ErrInvalidDraft
	}
	queries := dbgen.New(repository.pool)
	row, err := queries.MarkDraftConflict(ctx, dbgen.MarkDraftConflictParams{
		RemoteID: optionalText(remoteID), RemoteRevision: optionalText(remoteRevision), ID: draftID, AccountID: accountID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrDraftNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("mark draft conflict: %w", err)
	}
	return repository.hydrate(ctx, row)
}

func (repository *DraftRepositoryStore) DiscardDraft(ctx context.Context, user, account, draft string) (Draft, error) {
	userID, accountID, draftID, err := scopedResourceIDs(user, account, draft)
	if err != nil {
		return Draft{}, ErrInvalidDraft
	}
	row, err := dbgen.New(repository.pool).DiscardDraft(ctx, dbgen.DiscardDraftParams{ID: draftID, AccountID: accountID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrDraftNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("discard draft: %w", err)
	}
	return repository.hydrate(ctx, row)
}

func prepareDraftContent(input DraftContentInput, prior error) (DraftContentInput, error) {
	if prior != nil || !utf8.ValidString(input.Subject) || !utf8.ValidString(input.BodyText) || !utf8.ValidString(input.BodyHTML) || len(input.Subject) > 1<<20 || len(input.BodyText) > 10<<20 || len(input.BodyHTML) > 10<<20 || len(input.Recipients) > 512 || len(input.Attachments) > 128 {
		return DraftContentInput{}, ErrInvalidDraft
	}
	sanitized, err := sanitizeHTML(input.BodyHTML)
	if err != nil || len(sanitized) > 10<<20 {
		return DraftContentInput{}, ErrInvalidDraft
	}
	for _, recipient := range input.Recipients {
		if (recipient.Role != AddressTo && recipient.Role != AddressCC && recipient.Role != AddressBCC) || !boundedText(recipient.Address, 1024) || !utf8.ValidString(recipient.DisplayName) || len(strings.TrimSpace(recipient.DisplayName)) > 256 {
			return DraftContentInput{}, ErrInvalidDraft
		}
	}
	seenObjects := make(map[string]struct{}, len(input.Attachments))
	for _, attachment := range input.Attachments {
		if !draftObjectIDPattern.MatchString(attachment.ObjectID) || attachment.SizeBytes < 0 || !boundedText(attachment.MediaType, 255) || !utf8.ValidString(attachment.Filename) || len(strings.TrimSpace(attachment.Filename)) > 1024 {
			return DraftContentInput{}, ErrInvalidDraft
		}
		if _, duplicate := seenObjects[attachment.ObjectID]; duplicate {
			return DraftContentInput{}, ErrInvalidDraft
		}
		seenObjects[attachment.ObjectID] = struct{}{}
	}
	input.BodyHTML = sanitized
	return input, nil
}

func replaceDraftRelations(ctx context.Context, queries *dbgen.Queries, draftID, accountID pgtype.UUID, content DraftContentInput) error {
	if err := queries.DeleteDraftRecipients(ctx, dbgen.DeleteDraftRecipientsParams{DraftID: draftID, AccountID: accountID}); err != nil {
		return fmt.Errorf("replace draft recipients: %w", err)
	}
	positions := map[AddressRole]int32{AddressTo: 0, AddressCC: 0, AddressBCC: 0}
	for _, recipient := range normalizeAddresses(content.Recipients) {
		position := positions[recipient.Role]
		if err := queries.CreateDraftRecipient(ctx, dbgen.CreateDraftRecipientParams{
			DraftID: draftID, AccountID: accountID, Role: string(recipient.Role), Position: position,
			DisplayName: optionalText(pointerValue(recipient.DisplayName)), Address: recipient.Address,
		}); err != nil {
			return fmt.Errorf("create draft recipient: %w", err)
		}
		positions[recipient.Role] = position + 1
	}
	if err := queries.DeleteDraftAttachments(ctx, dbgen.DeleteDraftAttachmentsParams{DraftID: draftID, AccountID: accountID}); err != nil {
		return fmt.Errorf("replace draft attachments: %w", err)
	}
	for position, attachment := range content.Attachments {
		if err := queries.CreateDraftAttachment(ctx, dbgen.CreateDraftAttachmentParams{
			DraftID: draftID, AccountID: accountID, Position: int32(position), ObjectID: attachment.ObjectID,
			Filename: optionalText(attachment.Filename), MediaType: strings.ToLower(strings.TrimSpace(attachment.MediaType)), SizeBytes: attachment.SizeBytes,
		}); err != nil {
			return fmt.Errorf("create draft attachment: %w", err)
		}
	}
	return nil
}

func (repository *DraftRepositoryStore) hydrate(ctx context.Context, row dbgen.Draft) (Draft, error) {
	queries := dbgen.New(repository.pool)
	recipientRows, err := queries.ListDraftRecipients(ctx, dbgen.ListDraftRecipientsParams{DraftID: row.ID, AccountID: row.AccountID})
	if err != nil {
		return Draft{}, fmt.Errorf("list draft recipients: %w", err)
	}
	attachmentRows, err := queries.ListDraftAttachments(ctx, dbgen.ListDraftAttachmentsParams{DraftID: row.ID, AccountID: row.AccountID})
	if err != nil {
		return Draft{}, fmt.Errorf("list draft attachments: %w", err)
	}
	draft := mapDraft(row)
	draft.Recipients = make([]DraftRecipient, 0, len(recipientRows))
	for _, recipient := range recipientRows {
		draft.Recipients = append(draft.Recipients, DraftRecipient{Role: AddressRole(recipient.Role), Position: recipient.Position, DisplayName: textPointer(recipient.DisplayName), Address: recipient.Address})
	}
	draft.Attachments = make([]DraftAttachment, 0, len(attachmentRows))
	for _, attachment := range attachmentRows {
		draft.Attachments = append(draft.Attachments, DraftAttachment{Position: attachment.Position, ObjectID: attachment.ObjectID, Filename: textPointer(attachment.Filename), MediaType: attachment.MediaType, SizeBytes: attachment.SizeBytes})
	}
	return draft, nil
}

func mapDraft(row dbgen.Draft) Draft {
	return Draft{
		ID: uuid.UUID(row.ID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(),
		RemoteID: textPointer(row.RemoteID), RemoteRevision: textPointer(row.RemoteRevision),
		Subject: row.Subject, BodyText: row.BodyText, BodyHTML: row.BodyHtmlSanitized,
		LocalRevision: row.LocalRevision, SyncedRevision: row.SyncedRevision, SyncStatus: DraftSyncStatus(row.SyncStatus),
		RemoteCheckpointAt: row.RemoteCheckpointAt.Time.UTC(), LastRemoteSyncedAt: timePointer(row.LastRemoteSyncedAt),
		DiscardedAt: timePointer(row.DiscardedAt), CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}
