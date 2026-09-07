package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/gofiber/fiber/v3"
)

const defaultInboxPageSize = 50

type inboxCursor struct {
	LastMessageAt time.Time `json:"lastMessageAt"`
	ID            string    `json:"id"`
}

type messageCursor struct {
	SentAt time.Time `json:"sentAt"`
	ID     string    `json:"id"`
}

type searchCursor struct {
	Rank   float32   `json:"rank"`
	SentAt time.Time `json:"sentAt"`
	ID     string    `json:"id"`
}

type searchResult struct {
	ID              string    `json:"id"`
	ThreadID        string    `json:"threadId"`
	AccountID       string    `json:"accountId"`
	SenderName      string    `json:"senderName"`
	SenderAddress   string    `json:"senderAddress"`
	Subject         string    `json:"subject"`
	Preview         string    `json:"preview"`
	SentAt          time.Time `json:"sentAt"`
	IsRead          bool      `json:"isRead"`
	IsStarred       bool      `json:"isStarred"`
	IsImportant     bool      `json:"isImportant"`
	HasAttachment   bool      `json:"hasAttachment"`
	AttachmentCount int       `json:"attachmentCount"`
	Rank            float32   `json:"rank"`
}

type conversationThread struct {
	ID            string        `json:"id"`
	AccountID     string        `json:"accountId"`
	LastMessageAt time.Time     `json:"lastMessageAt"`
	IsRead        bool          `json:"isRead"`
	IsStarred     bool          `json:"isStarred"`
	IsImportant   bool          `json:"isImportant"`
	Category      mail.Category `json:"category"`
	MessageCount  int32         `json:"messageCount"`
	UnreadCount   int32         `json:"unreadCount"`
}

type conversationAttachment struct {
	ID          string  `json:"id"`
	Position    int32   `json:"position"`
	Filename    *string `json:"filename"`
	MediaType   string  `json:"mediaType"`
	Disposition string  `json:"disposition"`
	SizeBytes   int64   `json:"sizeBytes"`
}

type conversationMessage struct {
	ID          string                   `json:"id"`
	ThreadID    string                   `json:"threadId"`
	AccountID   string                   `json:"accountId"`
	Subject     string                   `json:"subject"`
	BodyText    string                   `json:"bodyText"`
	BodyHTML    string                   `json:"bodyHtml"`
	SentAt      time.Time                `json:"sentAt"`
	IsRead      bool                     `json:"isRead"`
	IsStarred   bool                     `json:"isStarred"`
	IsImportant bool                     `json:"isImportant"`
	Addresses   []mail.MessageAddress    `json:"addresses"`
	Attachments []conversationAttachment `json:"attachments"`
}

func listMailboxes(reader MailboxLabelReader) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, accountID, problem := mailScope(c, reader != nil)
		if problem != nil {
			return problem
		}
		items, err := reader.ListMailboxes(c.Context(), user.ID, accountID)
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "mailboxes_failed", "Mailboxes unavailable", "Mailboxes could not be loaded.")
		}
		return c.JSON(fiber.Map{"items": items})
	}
}

func listLabels(reader MailboxLabelReader) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, accountID, problem := mailScope(c, reader != nil)
		if problem != nil {
			return problem
		}
		items, err := reader.ListLabels(c.Context(), user.ID, accountID)
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "labels_failed", "Labels unavailable", "Labels could not be loaded.")
		}
		return c.JSON(fiber.Map{"items": items})
	}
}

func listInbox(reader InboxReader) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, accountID, problem := mailScope(c, reader != nil)
		if problem != nil {
			return problem
		}
		category := mail.Category(c.Query("category", string(mail.CategoryPrimary)))
		limit, err := strconv.Atoi(c.Query("limit", strconv.Itoa(defaultInboxPageSize)))
		if err != nil {
			return invalidInboxQuery()
		}
		cursor, err := decodeInboxCursor(c.Query("cursor"))
		if err != nil {
			return invalidInboxQuery()
		}
		page, err := reader.ListInbox(c.Context(), user.ID, accountID, category, cursor, limit)
		if errors.Is(err, mail.ErrInvalidThread) {
			return invalidInboxQuery()
		}
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "threads_failed", "Inbox unavailable", "Inbox threads could not be loaded.")
		}
		next, err := encodeInboxCursor(page.Next)
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "threads_failed", "Inbox unavailable", "Inbox pagination could not be created.")
		}
		return c.JSON(fiber.Map{"items": page.Items, "nextCursor": next})
	}
}

func getConversation(reader ThreadReader) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, accountID, problem := mailScope(c, reader != nil)
		if problem != nil {
			return problem
		}
		cursor, err := decodeMessageCursor(c.Query("cursor"))
		if err != nil {
			return invalidConversationQuery()
		}
		thread, err := reader.GetThread(c.Context(), user.ID, accountID, c.Params("threadId"))
		if errors.Is(err, mail.ErrInvalidThread) {
			return invalidConversationQuery()
		}
		if errors.Is(err, mail.ErrThreadNotFound) {
			return newProblem(fiber.StatusNotFound, "thread_not_found", "Thread not found", "The requested thread does not exist.")
		}
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "thread_failed", "Thread unavailable", "The conversation could not be loaded.")
		}
		page, err := reader.ListMessages(c.Context(), user.ID, accountID, thread.ID, cursor, 100)
		if errors.Is(err, mail.ErrInvalidMessage) {
			return invalidConversationQuery()
		}
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "thread_failed", "Thread unavailable", "Conversation messages could not be loaded.")
		}
		next, err := encodeMessageCursor(page.Next)
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "thread_failed", "Thread unavailable", "Conversation pagination could not be created.")
		}
		messages := make([]conversationMessage, 0, len(page.Items))
		for _, message := range page.Items {
			attachments := make([]conversationAttachment, 0, len(message.Attachments))
			for _, attachment := range message.Attachments {
				attachments = append(attachments, conversationAttachment{
					ID: attachment.ID, Position: attachment.Position, Filename: attachment.Filename,
					MediaType: attachment.MediaType, Disposition: attachment.Disposition, SizeBytes: attachment.SizeBytes,
				})
			}
			messages = append(messages, conversationMessage{
				ID: message.ID, ThreadID: message.ThreadID, AccountID: message.AccountID,
				Subject: message.Subject, BodyText: message.BodyText, BodyHTML: message.BodyHTML,
				SentAt: message.SentAt, IsRead: message.IsRead, IsStarred: message.IsStarred,
				IsImportant: message.IsImportant, Addresses: message.Addresses, Attachments: attachments,
			})
		}
		return c.JSON(fiber.Map{
			"thread": conversationThread{
				ID: thread.ID, AccountID: thread.AccountID, LastMessageAt: thread.LastMessageAt,
				IsRead: thread.IsRead, IsStarred: thread.IsStarred, IsImportant: thread.IsImportant,
				Category: thread.Category, MessageCount: thread.MessageCount, UnreadCount: thread.UnreadCount,
			},
			"messages": messages, "nextCursor": next,
		})
	}
}

func searchMail(reader SearchReader) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, accountID, problem := mailScope(c, reader != nil)
		if problem != nil {
			return problem
		}
		rawQuery := c.Query("q")
		if len(rawQuery) > 1024 {
			return invalidSearchQuery("query_too_long")
		}
		query, err := mail.ParseSearch(rawQuery)
		if err != nil {
			var validation *mail.SearchValidationError
			if errors.As(err, &validation) {
				return invalidSearchQuery(validation.Code)
			}
			return invalidSearchQuery("invalid_query")
		}
		limit, err := strconv.Atoi(c.Query("limit", strconv.Itoa(defaultInboxPageSize)))
		if err != nil || limit < 1 || limit > 100 {
			return invalidSearchQuery("invalid_limit")
		}
		cursor, err := decodeSearchCursor(c.Query("cursor"))
		if err != nil {
			return invalidSearchQuery("invalid_cursor")
		}
		page, err := reader.SearchMessages(c.Context(), user.ID, accountID, query, cursor, limit)
		var validation *mail.SearchValidationError
		if errors.As(err, &validation) {
			return invalidSearchQuery(validation.Code)
		}
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "search_failed", "Search unavailable", "Mail search could not be completed.")
		}
		items := make([]searchResult, 0, len(page.Items))
		for _, hit := range page.Items {
			message := hit.Message
			senderName, senderAddress := "", ""
			for _, address := range message.Addresses {
				if address.Role == mail.AddressFrom || address.Role == mail.AddressSender {
					senderAddress = address.Address
					if address.DisplayName != nil {
						senderName = *address.DisplayName
					}
					break
				}
			}
			if senderName == "" {
				senderName = senderAddress
			}
			preview := strings.Join(strings.Fields(message.BodyText), " ")
			if len([]rune(preview)) > 240 {
				preview = string([]rune(preview)[:240])
			}
			items = append(items, searchResult{
				ID: message.ID, ThreadID: message.ThreadID, AccountID: message.AccountID,
				SenderName: senderName, SenderAddress: senderAddress, Subject: message.Subject,
				Preview: preview, SentAt: message.SentAt, IsRead: message.IsRead,
				IsStarred: message.IsStarred, IsImportant: message.IsImportant,
				HasAttachment: len(message.Attachments) > 0, AttachmentCount: len(message.Attachments), Rank: hit.Rank,
			})
		}
		next, err := encodeSearchCursor(page.Next)
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "search_failed", "Search unavailable", "Search pagination could not be created.")
		}
		return c.JSON(fiber.Map{"items": items, "nextCursor": next})
	}
}

func mailScope(c fiber.Ctx, available bool) (authbridge.User, string, error) {
	user, ok := authbridge.UserFromContext(c.Context())
	if !ok {
		return authbridge.User{}, "", newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
	}
	if !available {
		return authbridge.User{}, "", newProblem(fiber.StatusServiceUnavailable, "mail_unavailable", "Mail unavailable", "Mail data is temporarily unavailable.")
	}
	accountID := c.Query("accountId")
	if accountID == "" {
		return authbridge.User{}, "", newProblem(fiber.StatusBadRequest, "invalid_mail_query", "Invalid mail query", "A valid accountId is required.")
	}
	return user, accountID, nil
}

func invalidInboxQuery() error {
	return newProblem(fiber.StatusBadRequest, "invalid_mail_query", "Invalid mail query", "The category, cursor, or page size is invalid.")
}

func invalidConversationQuery() error {
	return newProblem(fiber.StatusBadRequest, "invalid_thread_query", "Invalid thread query", "The account, thread, or cursor is invalid.")
}

func invalidSearchQuery(code string) error {
	return newProblem(fiber.StatusBadRequest, "search_"+code, "Invalid search query", "The search expression is invalid.")
}

func encodeInboxCursor(cursor *mail.ThreadCursor) (*string, error) {
	if cursor == nil {
		return nil, nil
	}
	payload, err := json.Marshal(inboxCursor{LastMessageAt: cursor.LastMessageAt, ID: cursor.ID})
	if err != nil {
		return nil, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return &encoded, nil
}

func decodeInboxCursor(encoded string) (*mail.ThreadCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > 768 {
		return nil, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) > 512 {
		return nil, errors.New("invalid cursor")
	}
	var cursor inboxCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.LastMessageAt.IsZero() || cursor.ID == "" {
		return nil, errors.New("invalid cursor")
	}
	return &mail.ThreadCursor{LastMessageAt: cursor.LastMessageAt, ID: cursor.ID}, nil
}

func encodeMessageCursor(cursor *mail.MessageCursor) (*string, error) {
	if cursor == nil {
		return nil, nil
	}
	payload, err := json.Marshal(messageCursor{SentAt: cursor.SentAt, ID: cursor.ID})
	if err != nil {
		return nil, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return &encoded, nil
}

func decodeMessageCursor(encoded string) (*mail.MessageCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > 768 {
		return nil, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) > 512 {
		return nil, errors.New("invalid cursor")
	}
	var cursor messageCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.SentAt.IsZero() || cursor.ID == "" {
		return nil, errors.New("invalid cursor")
	}
	return &mail.MessageCursor{SentAt: cursor.SentAt, ID: cursor.ID}, nil
}

func encodeSearchCursor(cursor *mail.SearchCursor) (*string, error) {
	if cursor == nil {
		return nil, nil
	}
	payload, err := json.Marshal(searchCursor{Rank: cursor.Rank, SentAt: cursor.SentAt, ID: cursor.ID})
	if err != nil {
		return nil, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return &encoded, nil
}

func decodeSearchCursor(encoded string) (*mail.SearchCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > 768 {
		return nil, errors.New("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) > 512 {
		return nil, errors.New("invalid cursor")
	}
	var cursor searchCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.SentAt.IsZero() || cursor.ID == "" {
		return nil, errors.New("invalid cursor")
	}
	return &mail.SearchCursor{Rank: cursor.Rank, SentAt: cursor.SentAt, ID: cursor.ID}, nil
}
