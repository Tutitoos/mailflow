package httpapi

import (
	"errors"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
	"github.com/gofiber/fiber/v3"
)

type draftRecipientRequest struct {
	Role        mail.AddressRole `json:"role"`
	DisplayName string           `json:"displayName,omitempty"`
	Address     string           `json:"address"`
}

type draftContentRequest struct {
	AccountID        string                   `json:"accountId"`
	ExpectedRevision int64                    `json:"expectedRevision,omitempty"`
	Subject          string                   `json:"subject"`
	BodyText         string                   `json:"bodyText"`
	BodyHTML         string                   `json:"bodyHtml"`
	Recipients       []draftRecipientRequest  `json:"recipients"`
	Attachments      []draftAttachmentRequest `json:"attachments"`
	Mode             mail.ComposeMode         `json:"mode"`
	SourceMessageID  string                   `json:"sourceMessageId,omitempty"`
}

type draftAttachmentRequest struct {
	ObjectID  string `json:"objectId"`
	Filename  string `json:"filename,omitempty"`
	MediaType string `json:"mediaType"`
	SizeBytes int64  `json:"sizeBytes"`
}

type draftAccountRequest struct {
	AccountID string `json:"accountId"`
}

type sendDraftRequest struct {
	AccountID        string `json:"accountId"`
	DraftID          string `json:"draftId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

func createDraft(service DraftService) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return authenticationProblem()
		}
		if service == nil {
			return draftUnavailable()
		}
		var request draftContentRequest
		if c.Bind().Body(&request) != nil || request.AccountID == "" {
			return invalidDraftProblem()
		}
		draft, err := service.SaveDraft(c.Context(), mail.CreateDraftInput{UserID: user.ID, AccountID: request.AccountID, Content: request.content(), Now: time.Now().UTC()})
		if err != nil {
			return mapDraftError(err)
		}
		return c.Status(fiber.StatusCreated).JSON(draft)
	}
}

func getDraft(service DraftService) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return authenticationProblem()
		}
		if service == nil {
			return draftUnavailable()
		}
		draft, err := service.GetDraft(c.Context(), user.ID, c.Query("accountId"), c.Params("draftId"))
		if err != nil {
			return mapDraftError(err)
		}
		return c.JSON(draft)
	}
}

func updateDraft(service DraftService) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return authenticationProblem()
		}
		if service == nil {
			return draftUnavailable()
		}
		var request draftContentRequest
		if c.Bind().Body(&request) != nil || request.AccountID == "" || request.ExpectedRevision < 1 {
			return invalidDraftProblem()
		}
		draft, err := service.UpdateDraft(c.Context(), mail.UpdateDraftInput{UserID: user.ID, AccountID: request.AccountID, DraftID: c.Params("draftId"), ExpectedRevision: request.ExpectedRevision, Content: request.content(), Now: time.Now().UTC()})
		if err != nil {
			return mapDraftError(err)
		}
		return c.JSON(draft)
	}
}

func checkpointDraft(service DraftService) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return authenticationProblem()
		}
		if service == nil {
			return draftUnavailable()
		}
		var request draftAccountRequest
		if c.Bind().Body(&request) != nil || request.AccountID == "" {
			return invalidDraftProblem()
		}
		draft, err := service.CheckpointDraft(c.Context(), user.ID, request.AccountID, c.Params("draftId"))
		if err != nil {
			return mapDraftError(err)
		}
		return c.JSON(draft)
	}
}

func discardDraft(service DraftService) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return authenticationProblem()
		}
		if service == nil {
			return draftUnavailable()
		}
		draft, err := service.DiscardDraft(c.Context(), user.ID, c.Query("accountId"), c.Params("draftId"))
		if err != nil {
			return mapDraftError(err)
		}
		return c.JSON(draft)
	}
}

func sendDraft(service DraftService) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return authenticationProblem()
		}
		if service == nil {
			return draftUnavailable()
		}
		key := strings.TrimSpace(c.Get("Idempotency-Key"))
		var request sendDraftRequest
		if c.Bind().Body(&request) != nil || request.AccountID == "" || request.DraftID == "" || request.ExpectedRevision < 1 || len(key) < 16 || len(key) > 96 {
			return invalidDraftProblem()
		}
		delivery, err := service.SendDraft(c.Context(), user.ID, request.AccountID, request.DraftID, request.ExpectedRevision, key)
		if err != nil {
			return mapDraftError(err)
		}
		return c.Status(fiber.StatusAccepted).JSON(delivery)
	}
}

func (request draftContentRequest) content() mail.DraftContentInput {
	recipients := make([]mail.MessageAddressInput, 0, len(request.Recipients))
	for _, recipient := range request.Recipients {
		recipients = append(recipients, mail.MessageAddressInput{Role: recipient.Role, DisplayName: recipient.DisplayName, Address: recipient.Address})
	}
	attachments := make([]mail.DraftAttachmentInput, 0, len(request.Attachments))
	for _, attachment := range request.Attachments {
		attachments = append(attachments, mail.DraftAttachmentInput{ObjectID: attachment.ObjectID, Filename: attachment.Filename, MediaType: attachment.MediaType, SizeBytes: attachment.SizeBytes})
	}
	return mail.DraftContentInput{Subject: request.Subject, BodyText: request.BodyText, BodyHTML: request.BodyHTML, Recipients: recipients, Attachments: attachments, Mode: request.Mode, SourceMessageID: request.SourceMessageID}
}

func mapDraftError(err error) error {
	switch {
	case errors.Is(err, mail.ErrInvalidDraft), errors.Is(err, mail.ErrInvalidDelivery):
		return invalidDraftProblem()
	case errors.Is(err, mail.ErrDraftNotFound):
		return newProblem(fiber.StatusNotFound, "draft_not_found", "Draft not found", "The requested draft is unavailable.")
	case errors.Is(err, mail.ErrDraftConflict), errors.Is(err, mail.ErrDeliveryConflict):
		return newProblem(fiber.StatusConflict, "draft_conflict", "Draft conflict", "The draft changed elsewhere or this delivery key belongs to different content.")
	default:
		return draftUnavailable()
	}
}

func authenticationProblem() error {
	return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
}

func invalidDraftProblem() error {
	return newProblem(fiber.StatusBadRequest, "invalid_draft", "Invalid draft", "The draft content or revision is invalid.")
}

func draftUnavailable() error {
	return newProblem(fiber.StatusServiceUnavailable, "drafts_unavailable", "Drafts unavailable", "Draft saving and delivery are temporarily unavailable.")
}
