package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
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
