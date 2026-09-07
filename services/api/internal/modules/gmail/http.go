package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

const maxResponseBytes = 32 << 20

type historyCursor struct {
	HistoryID string `json:"historyId"`
	PageToken string `json:"pageToken,omitempty"`
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
		return &ProviderError{Kind: ErrorPermanent, StatusCode: response.StatusCode}
	}
	return nil
}

func classify(status int, retryAfter string, payload ...[]byte) error {
	kind := ErrorPermanent
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = ErrorAuthorization
	case status == http.StatusTooManyRequests:
		kind = ErrorQuota
	case status >= 500:
		kind = ErrorTransient
	}
	if status == http.StatusForbidden && len(payload) > 0 && googleQuotaReason(payload[0]) {
		kind = ErrorQuota
	}
	return &ProviderError{Kind: kind, StatusCode: status, RetryAfter: parseRetryAfter(retryAfter)}
}

func googleQuotaReason(payload []byte) bool {
	var response struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &response) != nil {
		return false
	}
	for _, item := range response.Error.Errors {
		switch item.Reason {
		case "rateLimitExceeded", "userRateLimitExceeded", "quotaExceeded", "dailyLimitExceeded":
			return true
		}
	}
	return false
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
			ID           string `json:"id"`
			ThreadID     string `json:"threadId"`
			InternalDate string `json:"internalDate"`
			Raw          string `json:"raw"`
		}
		if err := provider.json(ctx, http.MethodGet, "/messages/"+url.PathEscape(id), url.Values{"format": {"raw"}}, nil, &response); err != nil {
			return mail.ChangePage{}, err
		}
		raw, err := base64.RawURLEncoding.DecodeString(response.Raw)
		if err != nil {
			return mail.ChangePage{}, &ProviderError{Kind: ErrorPermanent}
		}
		content, err := provider.normalizer.Normalize(bytes.NewReader(raw))
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
			return mail.ChangePage{}, &ProviderError{Kind: ErrorPermanent}
		}
		page.Messages = append(page.Messages, mail.RemoteMessage{RemoteID: response.ID, ThreadID: response.ThreadID, SentAt: time.UnixMilli(milliseconds).UTC(), Content: content})
	}
	return page, nil
}

func (provider *Provider) mapAttachmentIDs(ctx context.Context, messageID string, content *mail.NormalizedMessageContent) error {
	type part struct {
		Filename string `json:"filename"`
		MimeType string `json:"mimeType"`
		Body     struct {
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
		if current.Body.AttachmentID != "" {
			remoteIDs = append(remoteIDs, current.Body.AttachmentID)
		}
		for _, child := range current.Parts {
			visit(child)
		}
	}
	visit(response.Payload)
	if len(remoteIDs) != len(content.Attachments) {
		return &ProviderError{Kind: ErrorPermanent}
	}
	for index := range content.Attachments {
		content.Attachments[index].RemoteID = remoteIDs[index]
	}
	return nil
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
