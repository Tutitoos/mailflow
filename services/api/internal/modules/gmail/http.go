package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

// A RAW Gmail response base64url-encodes an RFC 2822 message whose MIME parts
// may already be base64 encoded. Keep the transport bound above that nested
// encoding overhead; the MIME normalizer still enforces the smaller content
// policy below.
const maxResponseBytes = 96 << 20
const maxDecodedPayloadBytes = 25 << 20
const rawAttachmentPrefix = "mailflow:google:raw-part:v1:"
const maxRawAttachmentIndex = 1023

type historyCursor struct {
	HistoryID string `json:"historyId"`
	PageToken string `json:"pageToken,omitempty"`
}

func (providerError *ProviderError) Is(target error) bool {
	return target == ErrHistoryExpired && providerError.StatusCode == http.StatusNotFound
}

func encodeCursor(cursor historyCursor) (mail.SyncCursor, error) {
	value, err := json.Marshal(cursor)
	return mail.SyncCursor{Kind: "google_history", Value: value}, err
}

func decodeHistoryCursor(cursor mail.SyncCursor) (historyCursor, error) {
	if cursor.Kind != "google_history" || len(cursor.Value) > 4096 {
		return historyCursor{}, ErrInvalidCursor
	}
	var value historyCursor
	if json.Unmarshal(cursor.Value, &value) != nil {
		return historyCursor{}, ErrInvalidCursor
	}
	return value, nil
}

func decodePageCursor(cursor mail.SyncCursor, kind string) (string, error) {
	if len(cursor.Value) == 0 && cursor.Kind == "" {
		return "", nil
	}
	if cursor.Kind != kind || len(cursor.Value) > 4096 {
		return "", ErrInvalidCursor
	}
	return string(cursor.Value), nil
}

func (provider *Provider) json(ctx context.Context, method, path string, query url.Values, body []byte, destination any) error {
	if err := provider.quota.Wait(ctx, quotaCost(method, path)); err != nil {
		return &ProviderError{Kind: ErrorTransient}
	}
	endpoint := provider.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return &ProviderError{Kind: ErrorPermanent}
	}
	request.Header.Set("Authorization", "Bearer "+provider.accessToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := provider.http.Do(request)
	if err != nil {
		return &ProviderError{Kind: ErrorTransient}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return classify(response.StatusCode, response.Header.Get("Retry-After"), payload)
	}
	if destination == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes+1))
	if err := decoder.Decode(destination); err != nil {
		return &ProviderError{Kind: ErrorPermanent, Reason: ReasonInvalidPayload, StatusCode: response.StatusCode}
	}
	return nil
}

func classify(status int, retryAfter string, payload ...[]byte) error {
	kind := ErrorPermanent
	reason := FailureReason("")
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = ErrorAuthorization
	case status == http.StatusTooManyRequests:
		kind = ErrorQuota
	case status >= 500:
		kind = ErrorTransient
	}
	if status == http.StatusForbidden && len(payload) > 0 {
		switch googleLimitReason(payload[0]) {
		case "dailyLimitExceeded":
			kind, reason = ErrorPermanent, ReasonDailyLimit
		case "rateLimitExceeded", "userRateLimitExceeded", "quotaExceeded":
			kind = ErrorQuota
		}
	}
	if kind == ErrorPermanent && reason == "" {
		if status == http.StatusNotFound {
			reason = ReasonNotFound
		} else {
			reason = ReasonRejected
		}
	}
	return &ProviderError{Kind: kind, Reason: reason, StatusCode: status, RetryAfter: parseRetryAfter(retryAfter)}
}

func googleLimitReason(payload []byte) string {
	var response struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &response) != nil {
		return ""
	}
	for _, item := range response.Error.Errors {
		switch item.Reason {
		case "rateLimitExceeded", "userRateLimitExceeded", "quotaExceeded", "dailyLimitExceeded":
			return item.Reason
		}
	}
	return ""
}

func parseRetryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if timestamp, err := http.ParseTime(value); err == nil {
		if delay := time.Until(timestamp); delay > 0 {
			return delay
		}
	}
	return 0
}

func (provider *Provider) loadMessages(ctx context.Context, ids []string) (mail.ChangePage, error) {
	page := mail.ChangePage{Messages: make([]mail.RemoteMessage, 0, len(ids))}
	for _, id := range ids {
		var response struct {
			ID           string   `json:"id"`
			ThreadID     string   `json:"threadId"`
			InternalDate string   `json:"internalDate"`
			LabelIDs     []string `json:"labelIds"`
			Raw          string   `json:"raw"`
		}
		if err := provider.json(ctx, http.MethodGet, "/messages/"+url.PathEscape(id), url.Values{"format": {"raw"}}, nil, &response); err != nil {
			return mail.ChangePage{}, err
		}
		content, err := provider.normalizeRawPayload(response.Raw, maxDecodedPayloadBytes)
		if err != nil {
			return mail.ChangePage{}, err
		}
		if len(content.Attachments) > 0 {
			if err := provider.mapAttachmentIDs(ctx, response.ID, &content); err != nil {
				return mail.ChangePage{}, err
			}
		}
		milliseconds, err := strconv.ParseInt(response.InternalDate, 10, 64)
		if err != nil || response.ID == "" || response.ThreadID == "" {
			return mail.ChangePage{}, &ProviderError{Kind: ErrorPermanent, Reason: ReasonInvalidEnvelope}
		}
		page.Messages = append(page.Messages, remoteMessage(response.ID, response.ThreadID, milliseconds, response.LabelIDs, content))
	}
	return page, nil
}

func (provider *Provider) normalizeRawPayload(encoded string, maxBytes int) (mail.NormalizedMessageContent, error) {
	if encodedPayloadExceedsDecodedLimit(encoded, maxBytes) {
		// Preserve only the provider envelope and state. The oversized raw MIME
		// body is deliberately neither decoded nor retained.
		return mail.NormalizedMessageContent{}, nil
	}
	raw, ok := decodeBase64URL(encoded, maxBytes)
	if !ok {
		return mail.NormalizedMessageContent{}, &ProviderError{Kind: ErrorPermanent, Reason: ReasonInvalidPayload}
	}
	content, err := provider.normalizer.Normalize(bytes.NewReader(raw))
	if err == nil {
		return content, nil
	}
	if !recoverableMIMEFailure(err) {
		return mail.NormalizedMessageContent{}, err
	}
	// Keep the provider envelope and state so one unsafe MIME payload cannot
	// pin the page cursor. A later reconciliation can replace the bodyless
	// placeholder after the normalizer learns how to handle the message safely.
	return mail.NormalizedMessageContent{}, nil
}

func recoverableMIMEFailure(err error) bool {
	return errors.Is(err, mail.ErrMalformedMIME) ||
		errors.Is(err, mail.ErrMIMETooLarge) ||
		errors.Is(err, mail.ErrTooManyParts)
}

func decodeBase64URL(value string, maxBytes int) ([]byte, bool) {
	if encodedPayloadExceedsDecodedLimit(value, maxBytes) {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(value)
	}
	if err != nil || len(decoded) > maxBytes {
		return nil, false
	}
	return decoded, true
}

func encodedPayloadExceedsDecodedLimit(value string, maxBytes int) bool {
	if maxBytes < 0 {
		return true
	}
	value = strings.TrimRight(value, "=")
	return len(value) > base64.RawURLEncoding.EncodedLen(maxBytes)
}

func remoteMessage(id, threadID string, milliseconds int64, labels []string, content mail.NormalizedMessageContent) mail.RemoteMessage {
	message := mail.RemoteMessage{RemoteID: id, ThreadID: threadID, SentAt: time.UnixMilli(milliseconds).UTC(), IsRead: true, Category: mail.CategoryPrimary, LabelIDs: append([]string(nil), labels...), Content: content}
	for _, label := range labels {
		switch label {
		case "UNREAD":
			message.IsRead = false
		case "STARRED":
			message.IsStarred = true
		case "IMPORTANT":
			message.IsImportant = true
		case "TRASH":
			message.InTrash = true
		case "CATEGORY_PROMOTIONS":
			message.Category = mail.CategoryPromotions
		case "CATEGORY_SOCIAL":
			message.Category = mail.CategorySocial
		case "CATEGORY_UPDATES":
			message.Category = mail.CategoryNotifications
		case "CATEGORY_FORUMS":
			message.Category = mail.CategoryForums
		}
	}
	return message
}

func (provider *Provider) mapAttachmentIDs(ctx context.Context, messageID string, content *mail.NormalizedMessageContent) error {
	type part struct {
		Filename string `json:"filename"`
		MimeType string `json:"mimeType"`
		Headers  []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
		Body struct {
			AttachmentID string `json:"attachmentId"`
		} `json:"body"`
		Parts []part `json:"parts"`
	}
	var response struct {
		Payload part `json:"payload"`
	}
	if err := provider.json(ctx, http.MethodGet, "/messages/"+url.PathEscape(messageID), url.Values{"format": {"full"}}, nil, &response); err != nil {
		return err
	}
	remoteIDs := make([]string, 0)
	var visit func(part)
	visit = func(current part) {
		hasAttachmentDisposition := false
		for _, header := range current.Headers {
			if strings.EqualFold(header.Name, "Content-Disposition") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(header.Value)), "attachment") {
				hasAttachmentDisposition = true
				break
			}
		}
		if gmailAttachmentCandidate(current.Filename, current.MimeType, current.Body.AttachmentID, hasAttachmentDisposition) {
			remoteID := current.Body.AttachmentID
			if remoteID == "" {
				remoteID = rawAttachmentPrefix + strconv.Itoa(len(remoteIDs))
			}
			remoteIDs = append(remoteIDs, remoteID)
		}
		for _, child := range current.Parts {
			visit(child)
		}
	}
	visit(response.Payload)
	if len(remoteIDs) != len(content.Attachments) {
		return &ProviderError{Kind: ErrorPermanent, Reason: ReasonAttachmentMapping}
	}
	for index := range content.Attachments {
		content.Attachments[index].RemoteID = remoteIDs[index]
	}
	return nil
}

func gmailAttachmentCandidate(filename, mediaType, attachmentID string, hasAttachmentDisposition bool) bool {
	if attachmentID != "" || filename != "" || hasAttachmentDisposition {
		return true
	}
	mediaType = strings.ToLower(strings.TrimSpace(strings.SplitN(mediaType, ";", 2)[0]))
	return mediaType != "" && mediaType != "text/plain" && mediaType != "text/html" && !strings.HasPrefix(mediaType, "multipart/")
}

func rawAttachmentIndex(remoteID string) (int, bool) {
	if !strings.HasPrefix(remoteID, rawAttachmentPrefix) {
		return 0, false
	}
	value := strings.TrimPrefix(remoteID, rawAttachmentPrefix)
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 || index > maxRawAttachmentIndex || value != strconv.Itoa(index) {
		return 0, false
	}
	return index, true
}

func encodeRaw(reader io.Reader) (string, error) {
	if reader == nil {
		return "", &ProviderError{Kind: ErrorPermanent}
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (35<<20)+1))
	if err != nil || len(raw) == 0 || len(raw) > 35<<20 {
		return "", &ProviderError{Kind: ErrorPermanent}
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
