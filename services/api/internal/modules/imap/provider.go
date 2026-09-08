package imap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	stdmail "net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

const (
	imapCursorKind = "imap_uid"
	imapPageSize   = 100
)

type Provider struct {
	user         string
	account      string
	credentials  storedCredentials
	capabilities map[string]bool
	store        ProviderStore
	protocol     Protocol
	normalizer   MessageNormalizer
}

type attachmentNormalizer interface {
	ExtractAttachment(io.Reader, int) ([]byte, mail.AttachmentInput, error)
}

type imapCursor struct {
	Position int                `json:"position"`
	Folders  []imapFolderCursor `json:"folders"`
}

type imapFolderCursor struct {
	RemoteID    string `json:"remoteId"`
	UIDValidity int64  `json:"uidValidity"`
	NextUID     int64  `json:"nextUid"`
}

type draftLocator struct {
	MailboxRemoteID string `json:"mailboxRemoteId"`
	WireName        string `json:"wireName"`
	UIDValidity     int64  `json:"uidValidity"`
	UID             int64  `json:"uid"`
}

func NewProvider(user, account string, rawCredentials []byte, capabilities map[string]bool, store ProviderStore, protocol Protocol, normalizer MessageNormalizer) (*Provider, error) {
	credentials, err := decodeStoredCredentials(rawCredentials)
	if err != nil || strings.TrimSpace(user) == "" || strings.TrimSpace(account) == "" || store == nil || protocol == nil || normalizer == nil {
		return nil, ErrInvalidConfiguration
	}
	return &Provider{
		user: user, account: account, credentials: credentials, store: store, protocol: protocol,
		normalizer: normalizer, capabilities: cloneCapabilities(capabilities),
	}, nil
}

func (provider *Provider) Kind() mail.ProviderKind { return mail.ProviderIMAP }

func (provider *Provider) Capabilities(context.Context) (map[string]bool, error) {
	return cloneCapabilities(provider.capabilities), nil
}

func (provider *Provider) Profile(ctx context.Context) (mail.ProviderProfile, error) {
	folders, err := provider.selectableFolders(ctx)
	if err != nil {
		return mail.ProviderProfile{}, err
	}
	cursor := imapCursor{Folders: make([]imapFolderCursor, 0, len(folders))}
	for _, folder := range folders {
		if folder.UIDValidity == nil || folder.UIDNext == nil {
			return mail.ProviderProfile{}, ErrInvalidCursor
		}
		cursor.Folders = append(cursor.Folders, imapFolderCursor{RemoteID: folder.RemoteID, UIDValidity: *folder.UIDValidity, NextUID: *folder.UIDNext})
	}
	encoded, err := encodeIMAPCursor(cursor)
	if err != nil {
		return mail.ProviderProfile{}, err
	}
	return mail.ProviderProfile{RemoteID: provider.account, Address: provider.credentials.Username, History: encoded}, nil
}

func (provider *Provider) Catalog(ctx context.Context, cursor mail.SyncCursor) (mail.CatalogPage, error) {
	if cursor.Kind != "" {
		return mail.CatalogPage{}, ErrInvalidCursor
	}
	folders, err := provider.store.Folders(ctx, provider.user, provider.account)
	if err != nil {
		return mail.CatalogPage{}, err
	}
	page := mail.CatalogPage{Mailboxes: make([]mail.RemoteMailbox, 0, len(folders))}
	for _, folder := range folders {
		page.Mailboxes = append(page.Mailboxes, mail.RemoteMailbox{
			RemoteID: folder.RemoteID, Name: folder.Name, Role: folder.Role, Selectable: folder.Selectable,
		})
	}
	return page, nil
}

func (provider *Provider) Backfill(ctx context.Context, cursor mail.SyncCursor, after, before *time.Time, limit int) (mail.ChangePage, error) {
	folders, err := provider.selectableFolders(ctx)
	if err != nil {
		return mail.ChangePage{}, err
	}
	state, err := decodeIMAPCursor(cursor)
	if err != nil {
		return mail.ChangePage{}, err
	}
	if len(state.Folders) == 0 {
		state.Folders = folderCursors(folders, 1)
	}
	if !cursorMatchesFolders(state, folders) || state.Position < 0 || state.Position > len(folders) {
		return mail.ChangePage{}, ErrInvalidCursor
	}
	if state.Position == len(folders) {
		return mail.ChangePage{NextCursor: mail.SyncCursor{}}, nil
	}
	if limit < 1 || limit > imapPageSize {
		limit = imapPageSize
	}
	position := state.Position
	folder := folders[position]
	result, err := provider.protocol.Fetch(ctx, provider.credentials, FetchRequest{
		Folder: folder, FromUID: state.Folders[position].NextUID, After: after, Before: before, Limit: limit,
		Snapshot: after == nil && before == nil,
	})
	if err != nil {
		return mail.ChangePage{}, err
	}
	messages, err := provider.normalizeFetched(folder, result.Messages)
	if err != nil {
		return mail.ChangePage{}, err
	}
	state.Folders[position].NextUID = result.NextUID
	if !result.HasMore {
		state.Position++
	}
	next, err := encodeIMAPCursor(state)
	if err != nil {
		return mail.ChangePage{}, err
	}
	page := mail.ChangePage{Messages: messages, NextCursor: next, HasMore: state.Position < len(folders) || result.HasMore}
	if !result.HasMore && result.SnapshotUIDs != nil {
		page.LocationSnapshots = []mail.RemoteLocationSnapshot{{MailboxID: folder.RemoteID, UIDValidity: pointerValue(folder.UIDValidity), PresentUIDs: result.SnapshotUIDs}}
	}
	return page, nil
}

func (provider *Provider) Changes(ctx context.Context, cursor mail.SyncCursor) (mail.ChangePage, error) {
	folders, err := provider.selectableFolders(ctx)
	if err != nil {
		return mail.ChangePage{}, err
	}
	state, err := decodeIMAPCursor(cursor)
	if err != nil || len(state.Folders) == 0 || !cursorMatchesFolders(state, folders) || state.Position < 0 || state.Position >= len(folders) {
		return mail.ChangePage{}, ErrInvalidCursor
	}
	position := state.Position
	folder := folders[position]
	result, err := provider.protocol.Fetch(ctx, provider.credentials, FetchRequest{Folder: folder, FromUID: state.Folders[position].NextUID, Limit: imapPageSize})
	if errors.Is(err, ErrUIDValidityChanged) {
		return mail.ChangePage{}, ErrUIDValidityChanged
	}
	if err != nil {
		return mail.ChangePage{}, err
	}
	messages, err := provider.normalizeFetched(folder, result.Messages)
	if err != nil {
		return mail.ChangePage{}, err
	}
	state.Folders[position].NextUID = result.NextUID
	if !result.HasMore {
		state.Position = (state.Position + 1) % len(folders)
	}
	next, err := encodeIMAPCursor(state)
	if err != nil {
		return mail.ChangePage{}, err
	}
	return mail.ChangePage{Messages: messages, NextCursor: next, HasMore: result.HasMore || state.Position != 0}, nil
}

func (provider *Provider) Apply(ctx context.Context, action mail.RemoteAction) error {
	if len(action.TargetIDs) == 0 || len(action.TargetIDs) > 1000 {
		return ErrInvalidMessageLocation
	}
	locations, err := provider.store.Locations(ctx, provider.user, provider.account, action.TargetIDs)
	if err != nil || len(locations) == 0 {
		return firstProviderError(err, ErrInvalidMessageLocation)
	}
	switch mail.ActionKind(action.Kind) {
	case mail.ActionMarkRead:
		return provider.setFlags(ctx, locations, []string{"\\Seen"}, nil)
	case mail.ActionMarkUnread:
		return provider.setFlags(ctx, locations, nil, []string{"\\Seen"})
	case mail.ActionStar:
		return provider.setFlags(ctx, locations, []string{"\\Flagged"}, nil)
	case mail.ActionUnstar:
		return provider.setFlags(ctx, locations, nil, []string{"\\Flagged"})
	case mail.ActionMarkImportant:
		return provider.setFlags(ctx, locations, []string{"$Important"}, nil)
	case mail.ActionMarkUnimportant:
		return provider.setFlags(ctx, locations, nil, []string{"$Important"})
	case mail.ActionMoveToTrash:
		return provider.move(ctx, locations, mail.MailboxTrash)
	case mail.ActionRestoreTrash:
		return provider.move(ctx, locations, mail.MailboxInbox)
	case mail.ActionArchive:
		return provider.move(ctx, locations, mail.MailboxArchive)
	default:
		return ErrActionUnsupported
	}
}

func (provider *Provider) SaveDraft(ctx context.Context, draft mail.OutgoingMessage) (string, error) {
	if !provider.capabilities["imap.uidplus"] {
		return "", ErrActionUnsupported
	}
	raw, err := readOutgoing(draft.Raw)
	if err != nil {
		return "", err
	}
	folder, err := provider.folderByRole(ctx, mail.MailboxDrafts)
	if err != nil {
		return "", err
	}
	appended, err := provider.protocol.Append(ctx, provider.credentials, folder, raw, []string{"\\Draft", "\\Seen"})
	if err != nil || appended.UID < 1 || appended.UIDValidity < 1 {
		return "", firstProviderError(err, ErrDeliveryAmbiguous)
	}
	if draft.DraftID != "" {
		if old, decodeErr := decodeDraftLocator(draft.DraftID); decodeErr == nil {
			_ = provider.protocol.MarkDeleted(ctx, provider.credentials, MessageLocation{MailboxRemoteID: old.MailboxRemoteID, WireName: old.WireName, UIDValidity: old.UIDValidity, UID: old.UID})
		}
	}
	return encodeDraftLocator(draftLocator{MailboxRemoteID: folder.RemoteID, WireName: folder.WireName, UIDValidity: appended.UIDValidity, UID: appended.UID})
}

func (provider *Provider) DeleteDraft(ctx context.Context, remoteID string) error {
	locator, err := decodeDraftLocator(remoteID)
	if err != nil {
		return err
	}
	return provider.protocol.MarkDeleted(ctx, provider.credentials, MessageLocation{MailboxRemoteID: locator.MailboxRemoteID, WireName: locator.WireName, UIDValidity: locator.UIDValidity, UID: locator.UID})
}

func (provider *Provider) Send(ctx context.Context, outgoing mail.OutgoingMessage) (string, error) {
	raw, err := readOutgoing(outgoing.Raw)
	if err != nil {
		return "", err
	}
	if err := provider.protocol.SendSMTP(ctx, provider.credentials, raw); err != nil {
		return "", ErrDeliveryAmbiguous
	}
	// SMTP acceptance is authoritative. Failure to append a local Sent copy must
	// never turn a confirmed delivery into a retry that can duplicate mail.
	if sent, folderErr := provider.folderByRole(ctx, mail.MailboxSent); folderErr == nil {
		_, _ = provider.protocol.Append(ctx, provider.credentials, sent, raw, []string{"\\Seen"})
	}
	return stableMessageID(raw, messageIDFromRaw(raw)), nil
}

func (provider *Provider) DownloadAttachment(ctx context.Context, messageID, attachmentID string) (io.ReadCloser, error) {
	index, err := parseAttachmentID(attachmentID)
	if err != nil {
		return nil, err
	}
	locations, err := provider.store.Locations(ctx, provider.user, provider.account, []string{messageID})
	if err != nil || len(locations) == 0 {
		return nil, firstProviderError(err, ErrInvalidMessageLocation)
	}
	raw, err := provider.protocol.RawMessage(ctx, provider.credentials, locations[0])
	if err != nil {
		return nil, err
	}
	extractor, ok := provider.normalizer.(attachmentNormalizer)
	if !ok {
		return nil, ErrCapability
	}
	payload, _, err := extractor.ExtractAttachment(bytes.NewReader(raw), index)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(payload)), nil
}

func (provider *Provider) normalizeFetched(folder FolderState, fetched []FetchedMessage) ([]mail.RemoteMessage, error) {
	result := make([]mail.RemoteMessage, 0, len(fetched))
	for _, item := range fetched {
		if item.UID < 1 || item.UIDValidity < 1 || item.UIDValidity != pointerValue(folder.UIDValidity) || len(item.Raw) == 0 {
			return nil, ErrInvalidMessageLocation
		}
		content, err := provider.normalizer.Normalize(bytes.NewReader(item.Raw))
		if err != nil {
			return nil, err
		}
		content.MessageID = validMessageIdentifier(content.MessageID)
		content.References = validMessageIdentifiers(content.References)
		content.InReplyTo = validMessageIdentifiers(content.InReplyTo)
		remoteID := stableMessageID(item.Raw, content.MessageID)
		for index := range content.Attachments {
			content.Attachments[index].RemoteID = attachmentID(index)
		}
		flags := normalizedFlagSet(item.Flags)
		sentAt := item.SentAt.UTC()
		if sentAt.IsZero() {
			sentAt = time.Unix(0, 0).UTC()
		}
		result = append(result, mail.RemoteMessage{
			RemoteID: remoteID, ThreadID: stableThreadID(content, remoteID), SentAt: sentAt,
			IsRead: flags["\\seen"], IsStarred: flags["\\flagged"], IsImportant: flags["$important"],
			Category: mail.CategoryPrimary, InTrash: folder.Role == mail.MailboxTrash,
			Locations: []mail.RemoteLocation{{MailboxID: folder.RemoteID, UIDValidity: item.UIDValidity, UID: item.UID}}, Content: content,
		})
	}
	return result, nil
}

func (provider *Provider) selectableFolders(ctx context.Context) ([]FolderState, error) {
	folders, err := provider.store.Folders(ctx, provider.user, provider.account)
	if err != nil {
		return nil, err
	}
	result := folders[:0]
	for _, folder := range folders {
		if folder.Selectable && folder.CursorState != FolderCursorMissing && folder.UIDValidity != nil && folder.UIDNext != nil {
			result = append(result, folder)
		}
	}
	if len(result) == 0 {
		return nil, ErrInvalidCursor
	}
	return result, nil
}

func (provider *Provider) folderByRole(ctx context.Context, role mail.MailboxRole) (FolderState, error) {
	folders, err := provider.selectableFolders(ctx)
	if err != nil {
		return FolderState{}, err
	}
	for _, folder := range folders {
		if folder.Role == role {
			return folder, nil
		}
	}
	return FolderState{}, ErrActionUnsupported
}

func (provider *Provider) setFlags(ctx context.Context, locations []MessageLocation, add, remove []string) error {
	for _, location := range locations {
		if err := provider.protocol.SetFlags(ctx, provider.credentials, location, add, remove); err != nil {
			return err
		}
	}
	return nil
}

func (provider *Provider) move(ctx context.Context, locations []MessageLocation, role mail.MailboxRole) error {
	if !provider.capabilities["imap.uidplus"] {
		return ErrActionUnsupported
	}
	destination, err := provider.folderByRole(ctx, role)
	if err != nil {
		return err
	}
	for _, location := range locations {
		if location.MailboxRemoteID == destination.RemoteID {
			continue
		}
		moved, moveErr := provider.protocol.Move(ctx, provider.credentials, location, destination, provider.capabilities)
		if moveErr != nil {
			return moveErr
		}
		next := MessageLocation{MessageRemoteID: location.MessageRemoteID, MailboxRemoteID: destination.RemoteID, WireName: destination.WireName, UIDValidity: moved.UIDValidity, UID: moved.UID}
		if err := provider.store.MoveLocation(ctx, provider.user, provider.account, location, next); err != nil {
			return err
		}
	}
	return nil
}

func folderCursors(folders []FolderState, next int64) []imapFolderCursor {
	result := make([]imapFolderCursor, 0, len(folders))
	for _, folder := range folders {
		result = append(result, imapFolderCursor{RemoteID: folder.RemoteID, UIDValidity: pointerValue(folder.UIDValidity), NextUID: next})
	}
	return result
}

func cursorMatchesFolders(cursor imapCursor, folders []FolderState) bool {
	if len(cursor.Folders) != len(folders) {
		return false
	}
	for index := range folders {
		if cursor.Folders[index].RemoteID != folders[index].RemoteID || cursor.Folders[index].UIDValidity != pointerValue(folders[index].UIDValidity) || cursor.Folders[index].NextUID < 1 {
			return false
		}
	}
	return true
}

func encodeIMAPCursor(value imapCursor) (mail.SyncCursor, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > 65536 {
		return mail.SyncCursor{}, ErrInvalidCursor
	}
	return mail.SyncCursor{Kind: imapCursorKind, Value: encoded}, nil
}

func decodeIMAPCursor(cursor mail.SyncCursor) (imapCursor, error) {
	if cursor.Kind == "" && len(cursor.Value) == 0 {
		return imapCursor{}, nil
	}
	if cursor.Kind != imapCursorKind || len(cursor.Value) == 0 || len(cursor.Value) > 65536 {
		return imapCursor{}, ErrInvalidCursor
	}
	var value imapCursor
	if json.Unmarshal(cursor.Value, &value) != nil || len(value.Folders) > maxDiscoveredFolders {
		return imapCursor{}, ErrInvalidCursor
	}
	return value, nil
}

func stableMessageID(raw []byte, messageID string) string {
	identity := strings.TrimSpace(messageID)
	if identity == "" {
		digest := sha256.Sum256(raw)
		return "imap:message:v1_" + base64.RawURLEncoding.EncodeToString(digest[:])
	}
	return stableIdentifier("message", identity)
}

func messageIDFromRaw(raw []byte) string {
	message, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(message.Header.Get("Message-ID"))
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
		value = value[1 : len(value)-1]
	}
	return validMessageIdentifier(value)
}

func stableThreadID(content mail.NormalizedMessageContent, remoteID string) string {
	root := ""
	if len(content.References) > 0 {
		root = content.References[0]
	} else if len(content.InReplyTo) > 0 {
		root = content.InReplyTo[0]
	} else if content.MessageID != "" {
		root = content.MessageID
	} else {
		root = remoteID
	}
	return stableIdentifier("thread", root)
}

func stableIdentifier(kind, value string) string {
	digest := sha256.Sum256([]byte(value))
	return "imap:" + kind + ":v1_" + base64.RawURLEncoding.EncodeToString(digest[:])
}

func normalizedFlagSet(flags []string) map[string]bool {
	result := make(map[string]bool, len(flags))
	for _, flag := range flags {
		result[strings.ToLower(strings.TrimSpace(flag))] = true
	}
	return result
}

func validMessageIdentifiers(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = validMessageIdentifier(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func validMessageIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 998 || !strings.Contains(value, "@") || strings.ContainsAny(value, " <>\t\r\n\x00") {
		return ""
	}
	return value
}

func attachmentID(index int) string { return "part:" + strconv.Itoa(index) }

func parseAttachmentID(value string) (int, error) {
	if !strings.HasPrefix(value, "part:") {
		return 0, ErrInvalidMessageLocation
	}
	index, err := strconv.Atoi(strings.TrimPrefix(value, "part:"))
	if err != nil || index < 0 || index > 255 {
		return 0, ErrInvalidMessageLocation
	}
	return index, nil
}

func encodeDraftLocator(locator draftLocator) (string, error) {
	encoded, err := json.Marshal(locator)
	if err != nil {
		return "", ErrInvalidMessageLocation
	}
	return "imap:draft:v1_" + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeDraftLocator(value string) (draftLocator, error) {
	const prefix = "imap:draft:v1_"
	if !strings.HasPrefix(value, prefix) || len(value) > 16384 {
		return draftLocator{}, ErrInvalidMessageLocation
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		return draftLocator{}, ErrInvalidMessageLocation
	}
	var locator draftLocator
	if json.Unmarshal(encoded, &locator) != nil || locator.MailboxRemoteID == "" || locator.WireName == "" || locator.UIDValidity < 1 || locator.UID < 1 {
		return draftLocator{}, ErrInvalidMessageLocation
	}
	return locator, nil
}

func readOutgoing(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, ErrDeliveryAmbiguous
	}
	payload, err := io.ReadAll(io.LimitReader(reader, int64(mail.DefaultMIMEPolicy().MaxRawBytes)+1))
	if err != nil || len(payload) == 0 || int64(len(payload)) > mail.DefaultMIMEPolicy().MaxRawBytes {
		return nil, ErrDeliveryAmbiguous
	}
	return payload, nil
}

func cloneCapabilities(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source)+8)
	for key, value := range source {
		result[key] = value
	}
	for _, key := range []string{"actions", "attachments", "folders", "search", "send", "threads"} {
		result[key] = true
	}
	result["drafts"] = source["imap.uidplus"]
	result["labels"] = false
	result["categories"] = false
	return result
}

func firstProviderError(candidate, fallback error) error {
	if candidate != nil {
		return candidate
	}
	return fallback
}

var _ mail.AccountProvider = (*Provider)(nil)
