package microsoftgraph

import (
	"encoding/json"
	"strings"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

const maxCursorBytes = 44 << 10

type catalogCursor struct {
	FoldersNext    string                      `json:"foldersNext,omitempty"`
	FolderQueue    []string                    `json:"folderQueue,omitempty"`
	FolderSeen     []string                    `json:"folderSeen,omitempty"`
	CategoriesNext string                      `json:"categoriesNext,omitempty"`
	FolderRoles    map[string]mail.MailboxRole `json:"folderRoles,omitempty"`
	FoldersStarted bool                        `json:"foldersStarted"`
	FoldersDone    bool                        `json:"foldersDone"`
	CategoriesDone bool                        `json:"categoriesDone"`
}

type pageCursor struct {
	Next    string `json:"next"`
	TrashID string `json:"trashId,omitempty"`
}

var standardFolders = []struct {
	WellKnown string
	Role      mail.MailboxRole
}{
	{"inbox", mail.MailboxInbox},
	{"sentitems", mail.MailboxSent},
	{"drafts", mail.MailboxDrafts},
	{"deleteditems", mail.MailboxTrash},
	{"junkemail", mail.MailboxJunk},
	{"archive", mail.MailboxArchive},
}

func mapFolder(folder graphFolder, role mail.MailboxRole) (mail.RemoteMailbox, bool) {
	id := strings.TrimSpace(folder.ID)
	name := strings.TrimSpace(folder.DisplayName)
	if id == "" || name == "" || folder.TotalItemCount < 0 || folder.UnreadItemCount < 0 || folder.UnreadItemCount > folder.TotalItemCount {
		return mail.RemoteMailbox{}, false
	}
	return mail.RemoteMailbox{
		RemoteID:    id,
		Name:        name,
		Role:        role,
		Selectable:  !folder.IsHidden,
		TotalCount:  folder.TotalItemCount,
		UnreadCount: folder.UnreadItemCount,
	}, true
}

func mapCategory(category graphCategory) (mail.RemoteLabel, bool) {
	name := strings.TrimSpace(category.DisplayName)
	if name == "" || len(name) > 256 {
		return mail.RemoteLabel{}, false
	}
	// Graph assigns categories to messages by displayName rather than category ID.
	return mail.RemoteLabel{RemoteID: name, Name: name, Kind: mail.LabelUser}, true
}

func encodeCatalogCursor(cursor catalogCursor) (mail.SyncCursor, error) {
	value, err := json.Marshal(cursor)
	return mail.SyncCursor{Kind: "microsoft_catalog", Value: value}, err
}

func decodeCatalogCursor(cursor mail.SyncCursor) (catalogCursor, error) {
	if cursor.Kind == "" && len(cursor.Value) == 0 {
		return catalogCursor{}, nil
	}
	if cursor.Kind != "microsoft_catalog" || len(cursor.Value) == 0 || len(cursor.Value) > maxCursorBytes {
		return catalogCursor{}, ErrInvalidCursor
	}
	var state catalogCursor
	if json.Unmarshal(cursor.Value, &state) != nil || (state.FoldersDone && (state.FoldersNext != "" || len(state.FolderQueue) != 0)) || (state.CategoriesDone && state.CategoriesNext != "") || !validFolderRoleCursor(state.FolderRoles) || !validFolderIDs(state.FolderQueue, 512) || !validFolderIDs(state.FolderSeen, 512) {
		return catalogCursor{}, ErrInvalidCursor
	}
	return state, nil
}

func validFolderIDs(values []string, limit int) bool {
	if len(values) > limit {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || len(value) > 512 {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validFolderRoleCursor(roles map[string]mail.MailboxRole) bool {
	if len(roles) > len(standardFolders) {
		return false
	}
	seen := make(map[mail.MailboxRole]bool, len(roles))
	for id, role := range roles {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id || len(id) > 512 || seen[role] {
			return false
		}
		valid := false
		for _, folder := range standardFolders {
			if role == folder.Role {
				valid = true
				break
			}
		}
		if !valid {
			return false
		}
		seen[role] = true
	}
	return true
}

func encodePageCursor(kind string, cursor pageCursor) (mail.SyncCursor, error) {
	value, err := json.Marshal(cursor)
	return mail.SyncCursor{Kind: kind, Value: value}, err
}

func decodePageCursor(cursor mail.SyncCursor, kind string) (pageCursor, error) {
	if cursor.Kind == "" && len(cursor.Value) == 0 {
		return pageCursor{}, nil
	}
	if cursor.Kind != kind || len(cursor.Value) == 0 || len(cursor.Value) > maxCursorBytes {
		return pageCursor{}, ErrInvalidCursor
	}
	var state pageCursor
	if json.Unmarshal(cursor.Value, &state) != nil || state.Next == "" || len(state.TrashID) > 512 || (state.TrashID != "" && strings.TrimSpace(state.TrashID) != state.TrashID) {
		return pageCursor{}, ErrInvalidCursor
	}
	return state, nil
}
