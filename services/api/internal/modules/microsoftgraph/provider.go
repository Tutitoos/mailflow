package microsoftgraph

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

const (
	defaultBaseURL        = "https://graph.microsoft.com/v1.0/me"
	folderMetadataSelect  = "id,displayName,childFolderCount,totalItemCount,unreadItemCount,isHidden"
	messageMetadataSelect = "id,conversationId,receivedDateTime,sentDateTime,isRead,flag,importance,categories,parentFolderId,hasAttachments"
)

type ErrorKind string

const (
	ErrorAuthorization ErrorKind = "authorization"
	ErrorQuota         ErrorKind = "quota"
	ErrorTransient     ErrorKind = "transient"
	ErrorPermanent     ErrorKind = "permanent"
)

var (
	ErrInvalidCursor = errors.New("invalid Microsoft Graph cursor")
)

type ProviderError struct {
	Kind       ErrorKind
	StatusCode int
	Code       string
	RetryAfter time.Duration
}

func (providerError *ProviderError) Error() string {
	return "microsoft graph provider " + string(providerError.Kind)
}

func (providerError *ProviderError) RetryDelay() time.Duration { return providerError.RetryAfter }

type Provider struct {
	accessToken string
	baseURL     *url.URL
	http        *http.Client
	normalizer  mail.MIMEMessageNormalizer

	folderMu    sync.RWMutex
	folderRoles map[string]mail.MailboxRole
}

func New(accessToken string, client *http.Client, normalizer mail.MIMEMessageNormalizer) (*Provider, error) {
	return NewWithBaseURL(accessToken, defaultBaseURL, client, normalizer)
}

func NewWithBaseURL(accessToken, baseURL string, client *http.Client, normalizer mail.MIMEMessageNormalizer) (*Provider, error) {
	parsed, err := url.Parse(baseURL)
	if strings.TrimSpace(accessToken) == "" || err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !secureEndpoint(parsed) || normalizer == nil {
		return nil, errors.New("microsoft graph provider configuration is invalid")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return &Provider{accessToken: accessToken, baseURL: parsed, http: client, normalizer: normalizer, folderRoles: make(map[string]mail.MailboxRole)}, nil
}

func secureEndpoint(endpoint *url.URL) bool {
	if endpoint.Scheme == "https" {
		return true
	}
	host := strings.ToLower(endpoint.Hostname())
	return endpoint.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
}

func (*Provider) Kind() mail.ProviderKind { return mail.ProviderMicrosoft }

func (*Provider) Capabilities(context.Context) (map[string]bool, error) {
	return map[string]bool{
		"attachments": true,
		"categories":  true,
		"drafts":      true,
		"folders":     true,
		"labels":      true,
		"search":      true,
		"send":        true,
		"threads":     true,
	}, nil
}

func (provider *Provider) Profile(ctx context.Context) (mail.ProviderProfile, error) {
	var response graphProfile
	if err := provider.json(ctx, http.MethodGet, "", url.Values{"$select": {"id,mail,userPrincipalName"}}, nil, "", &response); err != nil {
		return mail.ProviderProfile{}, err
	}
	address := strings.TrimSpace(response.Mail)
	if address == "" {
		address = strings.TrimSpace(response.UserPrincipalName)
	}
	if strings.TrimSpace(response.ID) == "" || address == "" {
		return mail.ProviderProfile{}, permanentError()
	}
	return mail.ProviderProfile{
		RemoteID: response.ID,
		Address:  address,
		History:  mail.SyncCursor{Kind: "microsoft_delta"},
	}, nil
}

func (provider *Provider) Catalog(ctx context.Context, cursor mail.SyncCursor) (mail.CatalogPage, error) {
	state, err := decodeCatalogCursor(cursor)
	if err != nil {
		return mail.CatalogPage{}, err
	}
	if state.FoldersNext != "" {
		state.FoldersNext, err = provider.relativeNextLink(state.FoldersNext)
		if err != nil {
			return mail.CatalogPage{}, err
		}
	}
	if state.CategoriesNext != "" {
		state.CategoriesNext, err = provider.relativeNextLink(state.CategoriesNext)
		if err != nil {
			return mail.CatalogPage{}, err
		}
	}
	provider.restoreFolderRoles(state.FolderRoles)
	if err := provider.ensureFolderRoles(ctx); err != nil {
		return mail.CatalogPage{}, err
	}
	page := mail.CatalogPage{Mailboxes: []mail.RemoteMailbox{}, Labels: []mail.RemoteLabel{}}
	if !state.FoldersDone {
		var response folderCollection
		path, query := state.FoldersNext, url.Values(nil)
		if path == "" {
			switch {
			case !state.FoldersStarted:
				path = "/mailFolders"
				state.FoldersStarted = true
			case len(state.FolderQueue) > 0:
				parent := state.FolderQueue[0]
				state.FolderQueue = state.FolderQueue[1:]
				path = "/mailFolders/" + url.PathEscape(parent) + "/childFolders"
			default:
				state.FoldersDone = true
			}
		}
		if !state.FoldersDone {
			query = url.Values{
				"$select":              {folderMetadataSelect},
				"$top":                 {"100"},
				"includeHiddenFolders": {"true"},
			}
			if state.FoldersNext != "" {
				query = nil
			}
			if err := provider.json(ctx, http.MethodGet, path, query, nil, "", &response); err != nil {
				return mail.CatalogPage{}, err
			}
			for _, folder := range response.Value {
				mapped, ok := mapFolder(folder, provider.roleForFolder(folder.ID))
				if !ok || folder.ChildFolderCount < 0 {
					return mail.CatalogPage{}, permanentError()
				}
				if containsString(state.FolderSeen, mapped.RemoteID) {
					continue
				}
				state.FolderSeen = append(state.FolderSeen, mapped.RemoteID)
				if !validFolderIDs(state.FolderSeen, 512) {
					return mail.CatalogPage{}, permanentError()
				}
				page.Mailboxes = append(page.Mailboxes, mapped)
				provider.rememberFolder(mapped.RemoteID, mapped.Role)
				if folder.ChildFolderCount > 0 {
					state.FolderQueue = append(state.FolderQueue, mapped.RemoteID)
					if !validFolderIDs(state.FolderQueue, 512) {
						return mail.CatalogPage{}, permanentError()
					}
				}
			}
			state.FoldersNext, err = provider.relativeNextLink(response.NextLink)
			if err != nil {
				return mail.CatalogPage{}, err
			}
			state.FoldersDone = state.FoldersNext == "" && len(state.FolderQueue) == 0
		}
	}
	if !state.CategoriesDone {
		var response categoryCollection
		path, query := state.CategoriesNext, url.Values(nil)
		if path == "" {
			path = "/outlook/masterCategories"
			query = url.Values{"$top": {"100"}}
		}
		if err := provider.json(ctx, http.MethodGet, path, query, nil, "", &response); err != nil {
			return mail.CatalogPage{}, err
		}
		for _, category := range response.Value {
			mapped, ok := mapCategory(category)
			if !ok {
				return mail.CatalogPage{}, permanentError()
			}
			page.Labels = append(page.Labels, mapped)
		}
		state.CategoriesNext, err = provider.relativeNextLink(response.NextLink)
		if err != nil {
			return mail.CatalogPage{}, err
		}
		state.CategoriesDone = state.CategoriesNext == ""
	}
	page.HasMore = !state.FoldersDone || !state.CategoriesDone
	if page.HasMore {
		state.FolderRoles = provider.snapshotFolderRoles()
		page.NextCursor, err = encodeCatalogCursor(state)
	}
	return page, err
}

func (provider *Provider) Backfill(ctx context.Context, cursor mail.SyncCursor, after, before *time.Time, limit int) (mail.ChangePage, error) {
	if (after == nil && before == nil) || (after != nil && after.IsZero()) || (before != nil && before.IsZero()) || (after != nil && before != nil && !after.Before(*before)) || limit < 1 || limit > 500 {
		return mail.ChangePage{}, ErrInvalidCursor
	}
	state, err := decodePageCursor(cursor, "microsoft_backfill")
	if err != nil {
		return mail.ChangePage{}, err
	}
	if state.Next != "" {
		state.Next, err = provider.relativeNextLink(state.Next)
		if err != nil {
			return mail.ChangePage{}, err
		}
	}
	if state.TrashID != "" {
		provider.rememberFolder(state.TrashID, mail.MailboxTrash)
	}
	path, query := state.Next, url.Values(nil)
	if path == "" {
		path = "/messages"
		filters := make([]string, 0, 2)
		if after != nil {
			filters = append(filters, "receivedDateTime ge "+after.UTC().Format(time.RFC3339))
		}
		if before != nil {
			filters = append(filters, "receivedDateTime lt "+before.UTC().Format(time.RFC3339))
		}
		query = url.Values{
			"$filter":  {strings.Join(filters, " and ")},
			"$orderby": {"receivedDateTime desc"},
			"$select":  {messageMetadataSelect},
			"$top":     {strconv.Itoa(limit)},
		}
	}
	var response messageCollection
	if err := provider.json(ctx, http.MethodGet, path, query, nil, "", &response); err != nil {
		return mail.ChangePage{}, err
	}
	trashID, err := provider.folderID(ctx, "deleteditems", mail.MailboxTrash)
	if err != nil {
		return mail.ChangePage{}, err
	}
	page := mail.ChangePage{Messages: make([]mail.RemoteMessage, 0, len(response.Value))}
	for _, item := range response.Value {
		message, err := provider.loadMessage(ctx, item, trashID)
		if err != nil {
			return mail.ChangePage{}, err
		}
		page.Messages = append(page.Messages, message)
	}
	state.Next, err = provider.relativeNextLink(response.NextLink)
	if err != nil {
		return mail.ChangePage{}, err
	}
	page.HasMore = state.Next != ""
	if page.HasMore {
		state.TrashID = trashID
		page.NextCursor, err = encodePageCursor("microsoft_backfill", state)
	}
	return page, err
}

func (provider *Provider) Apply(ctx context.Context, action mail.RemoteAction) error {
	if len(action.TargetIDs) == 0 || len(action.TargetIDs) > 5000 || (action.TargetKind != "message" && action.TargetKind != "thread") {
		return permanentError()
	}
	ids := make([]string, 0, len(action.TargetIDs))
	for _, target := range action.TargetIDs {
		if strings.TrimSpace(target) == "" || len(target) > 512 {
			return permanentError()
		}
		if action.TargetKind == "message" {
			ids = append(ids, target)
			continue
		}
		threadIDs, err := provider.conversationMessages(ctx, target)
		if err != nil {
			return err
		}
		ids = append(ids, threadIDs...)
	}
	ids = uniqueStrings(ids)
	for _, id := range ids {
		if err := provider.applyMessage(ctx, id, action.Kind, action.LabelIDs); err != nil {
			return err
		}
	}
	return nil
}

func (provider *Provider) SaveDraft(ctx context.Context, draft mail.OutgoingMessage) (string, error) {
	if len(draft.DraftID) > 512 || len(draft.ThreadID) > 512 {
		return "", permanentError()
	}
	encoded, err := encodeMIME(draft.Raw)
	if err != nil {
		return "", err
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := provider.json(ctx, http.MethodPost, "/messages", nil, []byte(encoded), "text/plain", &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.ID) == "" {
		return "", permanentError()
	}
	if draft.DraftID != "" && draft.DraftID != response.ID {
		if err := provider.json(ctx, http.MethodDelete, "/messages/"+url.PathEscape(draft.DraftID), nil, nil, "", nil); err != nil {
			// The new draft is authoritative. Returning it avoids duplicating drafts on a retry;
			// later reconciliation can remove an older orphan left by a failed cleanup.
			return response.ID, nil
		}
	}
	return response.ID, nil
}

func (provider *Provider) Send(ctx context.Context, message mail.OutgoingMessage) (string, error) {
	draftID, err := provider.SaveDraft(ctx, mail.OutgoingMessage{DraftID: message.DraftID, ThreadID: message.ThreadID, Raw: message.Raw})
	if err != nil {
		return "", err
	}
	if err := provider.json(ctx, http.MethodPost, "/messages/"+url.PathEscape(draftID)+"/send", nil, nil, "", nil); err != nil {
		return "", err
	}
	return draftID, nil
}

func (provider *Provider) DownloadAttachment(ctx context.Context, messageID, attachmentID string) (io.ReadCloser, error) {
	if strings.TrimSpace(messageID) == "" || strings.TrimSpace(attachmentID) == "" {
		return nil, permanentError()
	}
	response, err := provider.do(ctx, http.MethodGet, "/messages/"+url.PathEscape(messageID)+"/attachments/"+url.PathEscape(attachmentID)+"/$value", nil, nil, "")
	if err != nil {
		return nil, err
	}
	if response.ContentLength > 25<<20 {
		response.Body.Close()
		return nil, permanentError()
	}
	return &boundedReadCloser{reader: io.LimitReader(response.Body, (25<<20)+1), closer: response.Body, limit: 25 << 20}, nil
}

func (provider *Provider) loadMessage(ctx context.Context, item graphMessage, trashID string) (mail.RemoteMessage, error) {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ConversationID) == "" {
		return mail.RemoteMessage{}, permanentError()
	}
	sentAt := item.ReceivedDateTime
	if sentAt.IsZero() {
		sentAt = item.SentDateTime
	}
	if sentAt.IsZero() {
		return mail.RemoteMessage{}, permanentError()
	}
	response, err := provider.do(ctx, http.MethodGet, "/messages/"+url.PathEscape(item.ID)+"/$value", nil, nil, "")
	if err != nil {
		return mail.RemoteMessage{}, err
	}
	content, normalizeErr := provider.normalizer.Normalize(response.Body)
	closeErr := response.Body.Close()
	if normalizeErr != nil {
		return mail.RemoteMessage{}, normalizeErr
	}
	if closeErr != nil {
		return mail.RemoteMessage{}, &ProviderError{Kind: ErrorTransient}
	}
	if len(content.Attachments) > 0 {
		if err := provider.mapAttachmentIDs(ctx, item.ID, &content); err != nil {
			return mail.RemoteMessage{}, err
		}
	}
	labels := append([]string(nil), item.Categories...)
	sort.Strings(labels)
	return mail.RemoteMessage{
		RemoteID:    item.ID,
		ThreadID:    item.ConversationID,
		SentAt:      sentAt.UTC(),
		IsRead:      item.IsRead,
		IsStarred:   item.Flag.Status == "flagged" || item.Flag.Status == "complete",
		IsImportant: item.Importance == "high",
		Category:    mail.CategoryPrimary,
		InTrash:     trashID != "" && item.ParentFolderID == trashID,
		LabelIDs:    labels,
		Content:     content,
	}, nil
}

func (provider *Provider) mapAttachmentIDs(ctx context.Context, messageID string, content *mail.NormalizedMessageContent) error {
	path := "/messages/" + url.PathEscape(messageID) + "/attachments"
	query := url.Values{"$select": {"id,name,contentType,size,isInline,contentId"}, "$top": {"100"}}
	remote := make([]graphAttachment, 0, len(content.Attachments))
	for path != "" {
		var response attachmentCollection
		if err := provider.json(ctx, http.MethodGet, path, query, nil, "", &response); err != nil {
			return err
		}
		remote = append(remote, response.Value...)
		if len(remote) > 256 {
			return permanentError()
		}
		next, err := provider.relativeNextLink(response.NextLink)
		if err != nil {
			return err
		}
		path, query = next, nil
	}
	used := make([]bool, len(remote))
	for index := range content.Attachments {
		match := -1
		for candidate, item := range remote {
			if used[candidate] || strings.TrimSpace(item.ID) == "" {
				continue
			}
			attachment := content.Attachments[index]
			if item.Name == attachment.Filename && strings.EqualFold(item.ContentType, attachment.MediaType) && (item.Size == 0 || item.Size == attachment.SizeBytes) {
				match = candidate
				break
			}
		}
		if match < 0 {
			for candidate := range remote {
				if !used[candidate] && strings.TrimSpace(remote[candidate].ID) != "" {
					match = candidate
					break
				}
			}
		}
		if match < 0 {
			return permanentError()
		}
		used[match] = true
		content.Attachments[index].RemoteID = remote[match].ID
	}
	return nil
}

func (provider *Provider) conversationMessages(ctx context.Context, conversationID string) ([]string, error) {
	escaped := strings.ReplaceAll(conversationID, "'", "''")
	path := "/messages"
	query := url.Values{
		"$filter": {"conversationId eq '" + escaped + "'"},
		"$select": {"id"},
		"$top":    {"100"},
	}
	ids := make([]string, 0)
	for path != "" {
		var response messageCollection
		if err := provider.json(ctx, http.MethodGet, path, query, nil, "", &response); err != nil {
			return nil, err
		}
		for _, message := range response.Value {
			if strings.TrimSpace(message.ID) == "" {
				return nil, permanentError()
			}
			ids = append(ids, message.ID)
		}
		if len(ids) > 5000 {
			return nil, permanentError()
		}
		next, err := provider.relativeNextLink(response.NextLink)
		if err != nil {
			return nil, err
		}
		path, query = next, nil
	}
	return uniqueStrings(ids), nil
}

func (provider *Provider) applyMessage(ctx context.Context, id, kind string, labels []string) error {
	path := "/messages/" + url.PathEscape(id)
	var body any
	switch kind {
	case "mark_read":
		body = map[string]bool{"isRead": true}
	case "mark_unread":
		body = map[string]bool{"isRead": false}
	case "star":
		body = map[string]any{"flag": map[string]string{"flagStatus": "flagged"}}
	case "unstar":
		body = map[string]any{"flag": map[string]string{"flagStatus": "notFlagged"}}
	case "mark_important":
		body = map[string]string{"importance": "high"}
	case "mark_unimportant":
		body = map[string]string{"importance": "normal"}
	case "add_label", "remove_label":
		if !validCategoryNames(labels) {
			return permanentError()
		}
		var current struct {
			Categories []string `json:"categories"`
		}
		if err := provider.json(ctx, http.MethodGet, path, url.Values{"$select": {"categories"}}, nil, "", &current); err != nil {
			return err
		}
		categories := updateCategories(current.Categories, labels, kind == "add_label")
		body = map[string][]string{"categories": categories}
	case "move_to_trash", "restore_from_trash", "archive":
		destination := "deleteditems"
		if kind == "restore_from_trash" {
			destination = "inbox"
		} else if kind == "archive" {
			destination = "archive"
		}
		encoded, _ := json.Marshal(map[string]string{"destinationId": destination})
		return provider.json(ctx, http.MethodPost, path+"/move", nil, encoded, "application/json", nil)
	default:
		return permanentError()
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return permanentError()
	}
	return provider.json(ctx, http.MethodPatch, path, nil, encoded, "application/json", nil)
}

func (provider *Provider) folderID(ctx context.Context, wellKnown string, role mail.MailboxRole) (string, error) {
	provider.folderMu.RLock()
	for id, mappedRole := range provider.folderRoles {
		if mappedRole == role {
			provider.folderMu.RUnlock()
			return id, nil
		}
	}
	provider.folderMu.RUnlock()
	var response graphFolder
	if err := provider.json(ctx, http.MethodGet, "/mailFolders/"+wellKnown, url.Values{"$select": {"id"}}, nil, "", &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.ID) == "" {
		return "", permanentError()
	}
	provider.rememberFolder(response.ID, role)
	return response.ID, nil
}

func (provider *Provider) ensureFolderRoles(ctx context.Context) error {
	for _, folder := range standardFolders {
		if provider.hasFolderRole(folder.Role) {
			continue
		}
		if _, err := provider.folderID(ctx, folder.WellKnown, folder.Role); err != nil {
			return err
		}
	}
	return nil
}

func (provider *Provider) hasFolderRole(role mail.MailboxRole) bool {
	provider.folderMu.RLock()
	defer provider.folderMu.RUnlock()
	for _, current := range provider.folderRoles {
		if current == role {
			return true
		}
	}
	return false
}

func (provider *Provider) roleForFolder(id string) mail.MailboxRole {
	provider.folderMu.RLock()
	defer provider.folderMu.RUnlock()
	return provider.folderRoles[id]
}

func (provider *Provider) rememberFolder(id string, role mail.MailboxRole) {
	if id == "" || role == "" {
		return
	}
	provider.folderMu.Lock()
	provider.folderRoles[id] = role
	provider.folderMu.Unlock()
}

func (provider *Provider) restoreFolderRoles(roles map[string]mail.MailboxRole) {
	provider.folderMu.Lock()
	defer provider.folderMu.Unlock()
	for id, role := range roles {
		provider.folderRoles[id] = role
	}
}

func (provider *Provider) snapshotFolderRoles() map[string]mail.MailboxRole {
	provider.folderMu.RLock()
	defer provider.folderMu.RUnlock()
	roles := make(map[string]mail.MailboxRole, len(provider.folderRoles))
	for id, role := range provider.folderRoles {
		roles[id] = role
	}
	return roles
}

func encodeMIME(source io.Reader) (string, error) {
	if source == nil {
		return "", permanentError()
	}
	payload, err := io.ReadAll(io.LimitReader(source, (35<<20)+1))
	if err != nil || len(payload) == 0 || len(payload) > 35<<20 {
		return "", permanentError()
	}
	return base64.StdEncoding.EncodeToString(payload), nil
}

func updateCategories(current, requested []string, add bool) []string {
	values := make(map[string]string, len(current)+len(requested))
	for _, value := range current {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			values[strings.ToLower(trimmed)] = trimmed
		}
	}
	for _, value := range requested {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if add {
			if _, exists := values[key]; !exists {
				values[key] = trimmed
			}
		} else {
			delete(values, key)
		}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return strings.ToLower(result[left]) < strings.ToLower(result[right]) })
	return result
}

func validCategoryNames(values []string) bool {
	if len(values) == 0 || len(values) > 25 {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func permanentError() *ProviderError { return &ProviderError{Kind: ErrorPermanent} }

type boundedReadCloser struct {
	reader io.Reader
	closer io.Closer
	limit  int64
	read   int64
}

func (reader *boundedReadCloser) Read(destination []byte) (int, error) {
	count, err := reader.reader.Read(destination)
	reader.read += int64(count)
	if reader.read > reader.limit {
		return 0, permanentError()
	}
	return count, err
}

func (reader *boundedReadCloser) Close() error { return reader.closer.Close() }

type graphProfile struct {
	ID                string `json:"id"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
}

type graphFolder struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	ChildFolderCount int32  `json:"childFolderCount"`
	TotalItemCount   int32  `json:"totalItemCount"`
	UnreadItemCount  int32  `json:"unreadItemCount"`
	IsHidden         bool   `json:"isHidden"`
}

type folderCollection struct {
	Value    []graphFolder `json:"value"`
	NextLink string        `json:"@odata.nextLink"`
}

type graphCategory struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Color       string `json:"color"`
}

type categoryCollection struct {
	Value    []graphCategory `json:"value"`
	NextLink string          `json:"@odata.nextLink"`
}

type graphMessage struct {
	ID               string    `json:"id"`
	ConversationID   string    `json:"conversationId"`
	ReceivedDateTime time.Time `json:"receivedDateTime"`
	SentDateTime     time.Time `json:"sentDateTime"`
	IsRead           bool      `json:"isRead"`
	Flag             struct {
		Status string `json:"flagStatus"`
	} `json:"flag"`
	Importance     string   `json:"importance"`
	Categories     []string `json:"categories"`
	ParentFolderID string   `json:"parentFolderId"`
	HasAttachments bool     `json:"hasAttachments"`
}

type messageCollection struct {
	Value    []graphMessage `json:"value"`
	NextLink string         `json:"@odata.nextLink"`
}

type graphAttachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	IsInline    bool   `json:"isInline"`
	ContentID   string `json:"contentId"`
}

type attachmentCollection struct {
	Value    []graphAttachment `json:"value"`
	NextLink string            `json:"@odata.nextLink"`
}

var _ mail.Provider = (*Provider)(nil)
var _ mail.AttachmentProvider = (*Provider)(nil)
