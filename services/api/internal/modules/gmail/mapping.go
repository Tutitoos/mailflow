package gmail

import "github.com/Tutitoos/mailflow/services/api/internal/modules/mail"

type labelResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	MessagesTotal  int32  `json:"messagesTotal"`
	MessagesUnread int32  `json:"messagesUnread"`
	ThreadsTotal   int32  `json:"threadsTotal"`
	ThreadsUnread  int32  `json:"threadsUnread"`
}

var systemMailboxes = map[string]mail.MailboxRole{
	"INBOX": mail.MailboxInbox, "SENT": mail.MailboxSent, "DRAFT": mail.MailboxDrafts,
	"TRASH": mail.MailboxTrash, "SPAM": mail.MailboxJunk,
}

var categoryLabels = map[string]mail.Category{
	"CATEGORY_PERSONAL": mail.CategoryPrimary, "CATEGORY_PROMOTIONS": mail.CategoryPromotions,
	"CATEGORY_SOCIAL": mail.CategorySocial, "CATEGORY_UPDATES": mail.CategoryNotifications,
	"CATEGORY_FORUMS": mail.CategoryForums,
}

func mapMailbox(label labelResponse) (mail.RemoteMailbox, bool) {
	role, ok := systemMailboxes[label.ID]
	if !ok {
		return mail.RemoteMailbox{}, false
	}
	return mail.RemoteMailbox{RemoteID: label.ID, Name: label.Name, Role: role, Selectable: label.ID != "ALL", TotalCount: label.MessagesTotal, UnreadCount: label.MessagesUnread}, true
}

func mapLabel(label labelResponse) mail.RemoteLabel {
	kind := mail.LabelUser
	if label.Type == "system" {
		kind = mail.LabelSystem
	}
	var category *mail.Category
	if value, ok := categoryLabels[label.ID]; ok {
		kind = mail.LabelCategory
		category = &value
	}
	return mail.RemoteLabel{RemoteID: label.ID, Name: label.Name, Kind: kind, Category: category, TotalCount: label.MessagesTotal, UnreadCount: label.MessagesUnread}
}

func actionLabels(kind string) (add, remove []string, ok bool) {
	switch kind {
	case "mark_read":
		return nil, []string{"UNREAD"}, true
	case "mark_unread":
		return []string{"UNREAD"}, nil, true
	case "star":
		return []string{"STARRED"}, nil, true
	case "unstar":
		return nil, []string{"STARRED"}, true
	case "mark_important":
		return []string{"IMPORTANT"}, nil, true
	case "mark_unimportant":
		return nil, []string{"IMPORTANT"}, true
	case "move_to_trash":
		return []string{"TRASH"}, []string{"INBOX"}, true
	case "restore_from_trash":
		return []string{"INBOX"}, []string{"TRASH"}, true
	default:
		return nil, nil, false
	}
}
