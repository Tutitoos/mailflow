package microsoftgraph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

var ErrDeltaExpired = errors.New("Microsoft Graph delta cursor expired")

type deltaFolder struct {
	ID   string `json:"id"`
	Link string `json:"link,omitempty"`
}

type deltaCursor struct {
	Folders []deltaFolder `json:"folders"`
	Index   int           `json:"index,omitempty"`
}

type deltaMessage struct {
	graphMessage
	Removed *struct {
		Reason string `json:"reason"`
	} `json:"@removed,omitempty"`
}

type deltaCollection struct {
	Value     []deltaMessage `json:"value"`
	NextLink  string         `json:"@odata.nextLink"`
	DeltaLink string         `json:"@odata.deltaLink"`
}

// NewDeltaCursor creates the first per-folder checkpoint from an already
// discovered catalog. Folder IDs remain scoped to the owning account by the
// durable sync-run checkpoint and are never exposed in logs or metrics.
func NewDeltaCursor(folderIDs []string) (mail.SyncCursor, error) {
	if !validFolderIDs(folderIDs, 512) || len(folderIDs) == 0 {
		return mail.SyncCursor{}, ErrInvalidCursor
	}
	state := deltaCursor{Folders: make([]deltaFolder, 0, len(folderIDs))}
	for _, id := range folderIDs {
		state.Folders = append(state.Folders, deltaFolder{ID: id})
	}
	return encodeDeltaCursor(state)
}

func (provider *Provider) Changes(ctx context.Context, cursor mail.SyncCursor) (mail.ChangePage, error) {
	state, err := provider.decodeDeltaCursor(cursor)
	if err != nil {
		return mail.ChangePage{}, err
	}
	if len(state.Folders) == 0 {
		ids, discoveryErr := provider.discoverFolderIDs(ctx)
		if discoveryErr != nil {
			return mail.ChangePage{}, discoveryErr
		}
		initialized, cursorErr := NewDeltaCursor(ids)
		if cursorErr != nil {
			return mail.ChangePage{}, cursorErr
		}
		state, _ = provider.decodeDeltaCursor(initialized)
	}
	folder := &state.Folders[state.Index]
	path, query := folder.Link, url.Values(nil)
	if path == "" {
		path = "/mailFolders/" + url.PathEscape(folder.ID) + "/messages/delta"
		query = url.Values{
			"$select": {messageMetadataSelect},
			"$top":    {"100"},
		}
	}
	var response deltaCollection
	if err := provider.json(ctx, http.MethodGet, path, query, nil, "", &response); err != nil {
		if deltaExpired(err) {
			return mail.ChangePage{}, ErrDeltaExpired
		}
		return mail.ChangePage{}, err
	}
	if (response.NextLink == "") == (response.DeltaLink == "") {
		return mail.ChangePage{}, permanentError()
	}
	trashID, err := provider.folderID(ctx, "deleteditems", mail.MailboxTrash)
	if err != nil {
		return mail.ChangePage{}, err
	}
	page := mail.ChangePage{Messages: make([]mail.RemoteMessage, 0, len(response.Value))}
	for _, item := range response.Value {
		if strings.TrimSpace(item.ID) == "" {
			return mail.ChangePage{}, permanentError()
		}
		if item.Removed != nil {
			current, exists, resolveErr := provider.resolveRemovedMessage(ctx, item.ID, trashID)
			if resolveErr != nil {
				return mail.ChangePage{}, resolveErr
			}
			if exists {
				page.Messages = append(page.Messages, current)
			} else {
				page.DeletedRemoteIDs = append(page.DeletedRemoteIDs, item.ID)
			}
			continue
		}
		message, loadErr := provider.loadMessage(ctx, item.graphMessage, trashID)
		if loadErr != nil {
			return mail.ChangePage{}, loadErr
		}
		page.Messages = append(page.Messages, message)
	}
	if response.NextLink != "" {
		folder.Link, err = provider.deltaLink(folder.ID, response.NextLink)
		page.HasMore = true
	} else {
		folder.Link, err = provider.deltaLink(folder.ID, response.DeltaLink)
		state.Index++
		if state.Index == len(state.Folders) {
			state.Index = 0
			if err == nil {
				var added bool
				state.Folders, added, err = provider.refreshDeltaFolders(ctx, state.Folders)
				page.HasMore = added
			}
		} else {
			page.HasMore = true
		}
	}
	if err != nil {
		return mail.ChangePage{}, err
	}
	page.NextCursor, err = encodeDeltaCursor(state)
	return page, err
}

func (provider *Provider) refreshDeltaFolders(ctx context.Context, previous []deltaFolder) ([]deltaFolder, bool, error) {
	ids, err := provider.discoverFolderIDs(ctx)
	if err != nil {
		return nil, false, err
	}
	existing := make(map[string]deltaFolder, len(previous))
	for _, folder := range previous {
		existing[folder.ID] = folder
	}
	result := make([]deltaFolder, 0, len(ids))
	added := false
	for _, id := range ids {
		if folder, ok := existing[id]; ok {
			result = append(result, folder)
			continue
		}
		added = true
		result = append(result, deltaFolder{ID: id})
	}
	return result, added, nil
}

func (provider *Provider) resolveRemovedMessage(ctx context.Context, id, trashID string) (mail.RemoteMessage, bool, error) {
	var current graphMessage
	err := provider.json(ctx, http.MethodGet, "/messages/"+url.PathEscape(id), url.Values{"$select": {messageMetadataSelect}}, nil, "", &current)
	if err != nil {
		var providerError *ProviderError
		if errors.As(err, &providerError) && providerError.StatusCode == http.StatusNotFound {
			return mail.RemoteMessage{}, false, nil
		}
		return mail.RemoteMessage{}, false, err
	}
	message, err := provider.loadMessage(ctx, current, trashID)
	return message, err == nil, err
}

func (provider *Provider) discoverFolderIDs(ctx context.Context) ([]string, error) {
	queue := []string{"/mailFolders"}
	ids := make([]string, 0, 32)
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		query := url.Values{"$select": {folderMetadataSelect}, "$top": {"100"}, "includeHiddenFolders": {"true"}}
		for path != "" {
			var response folderCollection
			if err := provider.json(ctx, http.MethodGet, path, query, nil, "", &response); err != nil {
				return nil, err
			}
			for _, folder := range response.Value {
				if strings.TrimSpace(folder.ID) == "" || len(folder.ID) > 512 || folder.ChildFolderCount < 0 {
					return nil, permanentError()
				}
				ids = append(ids, folder.ID)
				if folder.ChildFolderCount > 0 {
					queue = append(queue, "/mailFolders/"+url.PathEscape(folder.ID)+"/childFolders")
				}
				if len(ids) > 512 || len(queue) > 512 {
					return nil, permanentError()
				}
			}
			next, err := provider.relativeNextLink(response.NextLink)
			if err != nil {
				return nil, err
			}
			path, query = next, nil
		}
	}
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return nil, permanentError()
	}
	return ids, nil
}

func encodeDeltaCursor(state deltaCursor) (mail.SyncCursor, error) {
	if !validDeltaState(state) {
		return mail.SyncCursor{}, ErrInvalidCursor
	}
	value, err := json.Marshal(state)
	if err != nil || len(value) > maxCursorBytes {
		return mail.SyncCursor{}, ErrInvalidCursor
	}
	return mail.SyncCursor{Kind: "microsoft_delta", Value: value}, nil
}

func (provider *Provider) decodeDeltaCursor(cursor mail.SyncCursor) (deltaCursor, error) {
	if cursor.Kind != "microsoft_delta" || len(cursor.Value) > maxCursorBytes {
		return deltaCursor{}, ErrInvalidCursor
	}
	if len(cursor.Value) == 0 {
		return deltaCursor{}, nil
	}
	var state deltaCursor
	if json.Unmarshal(cursor.Value, &state) != nil || !validDeltaState(state) {
		return deltaCursor{}, ErrInvalidCursor
	}
	for index := range state.Folders {
		if state.Folders[index].Link == "" {
			continue
		}
		state.Folders[index].Link, _ = provider.deltaLink(state.Folders[index].ID, state.Folders[index].Link)
		if state.Folders[index].Link == "" {
			return deltaCursor{}, ErrInvalidCursor
		}
	}
	return state, nil
}

func validDeltaState(state deltaCursor) bool {
	if len(state.Folders) == 0 {
		return state.Index == 0
	}
	if state.Index < 0 || state.Index >= len(state.Folders) || len(state.Folders) > 512 {
		return false
	}
	ids := make([]string, 0, len(state.Folders))
	for _, folder := range state.Folders {
		ids = append(ids, folder.ID)
		if len(folder.Link) > maxCursorBytes {
			return false
		}
	}
	return validFolderIDs(ids, 512)
}

func (provider *Provider) deltaLink(folderID, value string) (string, error) {
	relative, err := provider.relativeNextLink(value)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(relative)
	want := strings.TrimRight(provider.baseURL.Path, "/") + "/mailFolders/" + url.PathEscape(folderID) + "/messages/delta"
	query := parsed.Query()
	if err != nil || parsed.EscapedPath() != want || ((query.Get("$skiptoken") == "") == (query.Get("$deltatoken") == "")) {
		return "", ErrInvalidCursor
	}
	return relative, nil
}

func deltaExpired(err error) bool {
	var providerError *ProviderError
	if !errors.As(err, &providerError) {
		return false
	}
	return providerError.StatusCode == http.StatusGone || providerError.Code == "syncstatenotfound" || providerError.Code == "resyncrequired"
}
