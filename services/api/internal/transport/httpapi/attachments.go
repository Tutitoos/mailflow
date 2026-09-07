package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/gofiber/fiber/v3"
)

type AttachmentService interface {
	MaxAttachmentBytes() int64
	OpenMessageAttachment(context.Context, string, string, time.Time) (cdn.Attachment, *os.File, error)
	PutAttachment(context.Context, cdn.PutAttachmentInput) (cdn.Attachment, error)
}

func attachmentUpload(service AttachmentService) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		if service == nil {
			return attachmentUnavailable()
		}
		accountID := strings.TrimSpace(c.FormValue("accountId"))
		header, err := c.FormFile("file")
		if err != nil || accountID == "" || header.Size < 0 {
			return invalidAttachment()
		}
		if header.Size > service.MaxAttachmentBytes() {
			return newProblem(fiber.StatusRequestEntityTooLarge, "attachment_too_large", "Attachment too large", "The attachment exceeds this installation's configured limit.")
		}
		file, err := header.Open()
		if err != nil {
			return attachmentUnavailable()
		}
		defer file.Close()
		mediaType := header.Header.Get("Content-Type")
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		attachment, err := service.PutAttachment(c.Context(), cdn.PutAttachmentInput{
			UserID: user.ID, AccountID: accountID, Filename: header.Filename,
			MediaType: mediaType, Source: file, Now: time.Now().UTC(),
		})
		if errors.Is(err, cdn.ErrObjectTooLarge) {
			return newProblem(fiber.StatusRequestEntityTooLarge, "attachment_too_large", "Attachment too large", "The attachment exceeds this installation's configured limit.")
		}
		if errors.Is(err, cdn.ErrInvalidAttachment) || errors.Is(err, cdn.ErrInvalidMediaType) || errors.Is(err, cdn.ErrMediaTypeMismatch) {
			return invalidAttachment()
		}
		if errors.Is(err, cdn.ErrAttachmentNotFound) {
			return newProblem(fiber.StatusNotFound, "account_not_found", "Account not found", "The account does not exist or is not available to this user.")
		}
		if err != nil {
			return attachmentUnavailable()
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"objectId": attachment.ObjectID, "filename": attachment.Filename,
			"mediaType": attachment.MediaType, "sizeBytes": attachment.SizeBytes,
		})
	}
}

func attachmentDownload(service AttachmentService) fiber.Handler {
	return func(c fiber.Ctx) error {
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		if service == nil {
			return newProblem(fiber.StatusServiceUnavailable, "attachments_unavailable", "Attachments unavailable", "Attachment storage is temporarily unavailable.")
		}
		attachment, file, err := service.OpenMessageAttachment(c.Context(), user.ID, c.Params("attachmentId"), time.Now().UTC())
		if errors.Is(err, cdn.ErrAttachmentNotFound) {
			return newProblem(fiber.StatusNotFound, "attachment_not_found", "Attachment not found", "The attachment does not exist or is not available to this user.")
		}
		if errors.Is(err, cdn.ErrAttachmentMissing) {
			return newProblem(fiber.StatusConflict, "attachment_recovery_required", "Attachment unavailable", "The cached attachment must be recovered from its provider.")
		}
		if errors.Is(err, cdn.ErrAttachmentUnavailable) {
			return attachmentUnavailable()
		}
		if errors.Is(err, cdn.ErrObjectTooLarge) {
			return newProblem(fiber.StatusRequestEntityTooLarge, "attachment_too_large", "Attachment too large", "The attachment exceeds this installation's configured limit.")
		}
		if err != nil {
			return newProblem(fiber.StatusInternalServerError, "attachment_failed", "Attachment unavailable", "The attachment could not be opened.")
		}
		etag := `"` + attachment.ETag + `"`
		c.Set(fiber.HeaderETag, etag)
		c.Set(fiber.HeaderAcceptRanges, "bytes")
		c.Set(fiber.HeaderCacheControl, "private, max-age=0, must-revalidate")
		c.Set(fiber.HeaderContentType, attachment.MediaType)
		filename := "attachment"
		if attachment.Filename != nil {
			filename = *attachment.Filename
		}
		c.Set(fiber.HeaderContentDisposition, mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		if matchesETag(c.Get(fiber.HeaderIfNoneMatch), etag) {
			_ = file.Close()
			return c.SendStatus(fiber.StatusNotModified)
		}
		start, end, partial, err := parseByteRange(c.Get(fiber.HeaderRange), attachment.SizeBytes)
		if err != nil {
			_ = file.Close()
			c.Set(fiber.HeaderContentRange, fmt.Sprintf("bytes */%d", attachment.SizeBytes))
			return newProblem(fiber.StatusRequestedRangeNotSatisfiable, "invalid_range", "Invalid range", "The requested byte range cannot be served.")
		}
		length := end - start + 1
		if partial {
			c.Status(fiber.StatusPartialContent)
			c.Set(fiber.HeaderContentRange, fmt.Sprintf("bytes %d-%d/%d", start, end, attachment.SizeBytes))
		}
		if length == 0 {
			_ = file.Close()
			return c.Send(nil)
		}
		stream := &closingSectionReader{reader: io.NewSectionReader(file, start, length), file: file, remaining: length}
		if err := c.SendStream(stream, int(length)); err != nil {
			_ = file.Close()
			return err
		}
		return nil
	}
}

func invalidAttachment() error {
	return newProblem(fiber.StatusBadRequest, "invalid_attachment", "Invalid attachment", "The attachment metadata or content is invalid.")
}

func attachmentUnavailable() error {
	return newProblem(fiber.StatusServiceUnavailable, "attachments_unavailable", "Attachments unavailable", "Attachment storage is temporarily unavailable.")
}

type closingSectionReader struct {
	reader    *io.SectionReader
	file      *os.File
	remaining int64
}

func (reader *closingSectionReader) Read(destination []byte) (int, error) {
	count, err := reader.reader.Read(destination)
	reader.remaining -= int64(count)
	if err != nil || reader.remaining == 0 {
		_ = reader.file.Close()
	}
	return count, err
}

func matchesETag(header, etag string) bool {
	for value := range strings.SplitSeq(header, ",") {
		trimmed := strings.TrimSpace(value)
		if trimmed == "*" || trimmed == etag || strings.TrimPrefix(trimmed, "W/") == etag {
			return true
		}
	}
	return false
}

func parseByteRange(header string, size int64) (int64, int64, bool, error) {
	if header == "" {
		if size == 0 {
			return 0, -1, false, nil
		}
		return 0, size - 1, false, nil
	}
	if size <= 0 || !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") {
		return 0, 0, false, errors.New("unsupported range")
	}
	parts := strings.Split(strings.TrimPrefix(header, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, false, errors.New("invalid range")
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, false, errors.New("invalid suffix")
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true, nil
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false, errors.New("invalid start")
	}
	end := size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, false, errors.New("invalid end")
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true, nil
}
