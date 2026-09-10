package gmail

import (
	"bytes"
	"context"
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

const defaultBaseURL = "https://gmail.googleapis.com/gmail/v1/users/me"

type ErrorKind string

type FailureReason string

const (
	ErrorAuthorization ErrorKind = "authorization"
	ErrorQuota         ErrorKind = "quota"
	ErrorTransient     ErrorKind = "transient"
	ErrorPermanent     ErrorKind = "permanent"
)

const (
	ReasonNotFound          FailureReason = "not_found"
	ReasonRejected          FailureReason = "rejected"
	ReasonInvalidPayload    FailureReason = "invalid_payload"
	ReasonAttachmentMapping FailureReason = "attachment_mapping"
	ReasonInvalidEnvelope   FailureReason = "invalid_envelope"
)

var ErrInvalidCursor = errors.New("invalid Gmail cursor")
var ErrHistoryExpired = errors.New("Gmail history cursor expired")

type ProviderError struct {
	Kind       ErrorKind
	Reason     FailureReason
	StatusCode int
	RetryAfter time.Duration
}

func (providerError *ProviderError) Error() string {
	return "gmail provider " + string(providerError.Kind)
}

// SyncFailureCategory exposes only the bounded operational class needed by the
// synchronization run. It deliberately omits status codes and provider data.
func (providerError *ProviderError) SyncFailureCategory() string {
	switch providerError.Kind {
	case ErrorAuthorization, ErrorQuota, ErrorTransient, ErrorPermanent:
		return string(providerError.Kind)
	default:
		return ""
	}
}

// SyncFailureReason exposes a closed operational reason only. Provider
// responses, identifiers, URLs, and message content never cross this boundary.
func (providerError *ProviderError) SyncFailureReason() string {
	switch providerError.Reason {
	case ReasonNotFound, ReasonRejected, ReasonInvalidPayload, ReasonAttachmentMapping, ReasonInvalidEnvelope:
		return string(providerError.Reason)
	default:
		return ""
	}
}

type Provider struct {
	accessToken string
	baseURL     string
	http        *http.Client
	normalizer  mail.MIMEMessageNormalizer
	quota       QuotaLimiter
}

type rawAttachmentExtractor interface {
	ExtractAttachment(io.Reader, int) ([]byte, mail.AttachmentInput, error)
}

func New(accessToken string, client *http.Client, normalizer mail.MIMEMessageNormalizer) (*Provider, error) {
	return NewWithQuotaLimiter(accessToken, client, normalizer, NewQuotaLimiter())
}

func NewWithBaseURL(accessToken, baseURL string, client *http.Client, normalizer mail.MIMEMessageNormalizer) (*Provider, error) {
	return newProvider(accessToken, baseURL, client, normalizer, NewQuotaLimiter())
}

func NewWithQuotaLimiter(accessToken string, client *http.Client, normalizer mail.MIMEMessageNormalizer, limiter QuotaLimiter) (*Provider, error) {
	return newProvider(accessToken, defaultBaseURL, client, normalizer, limiter)
}

func newProvider(accessToken, baseURL string, client *http.Client, normalizer mail.MIMEMessageNormalizer, limiter QuotaLimiter) (*Provider, error) {
	parsed, err := url.Parse(baseURL)
	if accessToken == "" || err != nil || !parsed.IsAbs() || normalizer == nil || limiter == nil {
		return nil, errors.New("gmail provider configuration is invalid")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Provider{accessToken: accessToken, baseURL: strings.TrimRight(baseURL, "/"), http: client, normalizer: normalizer, quota: limiter}, nil
}

func (*Provider) Kind() mail.ProviderKind { return mail.ProviderGoogle }

func (*Provider) Capabilities(context.Context) (map[string]bool, error) {
	return map[string]bool{"actions": true, "attachments": true, "categories": true, "drafts": true, "folders": true, "labels": true, "search": true, "send": true, "threads": true}, nil
}

func (provider *Provider) Profile(ctx context.Context) (mail.ProviderProfile, error) {
	var profile struct {
		EmailAddress string `json:"emailAddress"`
		HistoryID    string `json:"historyId"`
	}
	if err := provider.json(ctx, http.MethodGet, "/profile", nil, nil, &profile); err != nil {
		return mail.ProviderProfile{}, err
	}
	if profile.EmailAddress == "" || profile.HistoryID == "" {
		return mail.ProviderProfile{}, &ProviderError{Kind: ErrorPermanent}
	}
	cursor, _ := encodeCursor(historyCursor{HistoryID: profile.HistoryID})
	return mail.ProviderProfile{RemoteID: profile.EmailAddress, Address: profile.EmailAddress, History: cursor}, nil
}

func (provider *Provider) Catalog(ctx context.Context, cursor mail.SyncCursor) (mail.CatalogPage, error) {
	if len(cursor.Value) != 0 {
		return mail.CatalogPage{}, ErrInvalidCursor
	}
	var response struct {
		Labels []labelResponse `json:"labels"`
	}
	if err := provider.json(ctx, http.MethodGet, "/labels", nil, nil, &response); err != nil {
		return mail.CatalogPage{}, err
	}
	page := mail.CatalogPage{Mailboxes: []mail.RemoteMailbox{}, Labels: []mail.RemoteLabel{}}
	for _, label := range response.Labels {
		if mailbox, ok := mapMailbox(label); ok {
			page.Mailboxes = append(page.Mailboxes, mailbox)
			continue
		}
		page.Labels = append(page.Labels, mapLabel(label))
	}
	return page, nil
}

func (provider *Provider) Changes(ctx context.Context, cursor mail.SyncCursor) (mail.ChangePage, error) {
	parsed, err := decodeHistoryCursor(cursor)
	if err != nil || parsed.HistoryID == "" {
		return mail.ChangePage{}, ErrInvalidCursor
	}
	query := url.Values{"startHistoryId": {parsed.HistoryID}, "maxResults": {"100"}}
	if parsed.PageToken != "" {
		query.Set("pageToken", parsed.PageToken)
	}
	var response struct {
		History []struct {
			MessagesAdded []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			} `json:"messagesAdded"`
			MessagesDeleted []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			} `json:"messagesDeleted"`
			LabelsAdded []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			} `json:"labelsAdded"`
			LabelsRemoved []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			} `json:"labelsRemoved"`
		} `json:"history"`
		HistoryID     string `json:"historyId"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := provider.json(ctx, http.MethodGet, "/history", query, nil, &response); err != nil {
		return mail.ChangePage{}, err
	}
	ids := make([]string, 0)
	deleted := make([]string, 0)
	seen := make(map[string]struct{})
	deletedSeen := make(map[string]struct{})
	for _, history := range response.History {
		for _, added := range history.MessagesAdded {
			if added.Message.ID != "" {
				if _, exists := seen[added.Message.ID]; !exists {
					seen[added.Message.ID] = struct{}{}
					ids = append(ids, added.Message.ID)
				}
			}
		}
		for _, changed := range append(history.LabelsAdded, history.LabelsRemoved...) {
			if changed.Message.ID != "" {
				if _, exists := seen[changed.Message.ID]; !exists {
					seen[changed.Message.ID] = struct{}{}
					ids = append(ids, changed.Message.ID)
				}
			}
		}
		for _, removed := range history.MessagesDeleted {
			if removed.Message.ID != "" {
				if _, exists := deletedSeen[removed.Message.ID]; !exists {
					deletedSeen[removed.Message.ID] = struct{}{}
					deleted = append(deleted, removed.Message.ID)
				}
			}
		}
	}
	if len(deletedSeen) > 0 {
		filtered := ids[:0]
		for _, id := range ids {
			if _, deletedOnPage := deletedSeen[id]; !deletedOnPage {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	page, err := provider.loadMessages(ctx, ids)
	if err != nil {
		return mail.ChangePage{}, err
	}
	nextHistory := response.HistoryID
	if nextHistory == "" {
		nextHistory = parsed.HistoryID
	}
	page.NextCursor, _ = encodeCursor(historyCursor{HistoryID: nextHistory, PageToken: response.NextPageToken})
	page.DeletedRemoteIDs = deleted
	page.HasMore = response.NextPageToken != ""
	return page, nil
}

func (provider *Provider) Backfill(ctx context.Context, cursor mail.SyncCursor, after, before *time.Time, limit int) (mail.ChangePage, error) {
	if (after == nil && before == nil) || (after != nil && after.IsZero()) || (before != nil && before.IsZero()) || (after != nil && before != nil && !after.Before(*before)) || limit < 1 || limit > 500 {
		return mail.ChangePage{}, ErrInvalidCursor
	}
	pageToken, err := decodePageCursor(cursor, "google_backfill")
	if err != nil {
		return mail.ChangePage{}, err
	}
	terms := make([]string, 0, 2)
	if after != nil {
		terms = append(terms, "after:"+strconv.FormatInt(after.UTC().Unix(), 10))
	}
	if before != nil {
		terms = append(terms, "before:"+strconv.FormatInt(before.UTC().Unix(), 10))
	}
	query := url.Values{"maxResults": {strconv.Itoa(limit)}, "q": {strings.Join(terms, " ")}}
	if pageToken != "" {
		query.Set("pageToken", pageToken)
	}
	var response struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := provider.json(ctx, http.MethodGet, "/messages", query, nil, &response); err != nil {
		return mail.ChangePage{}, err
	}
	ids := make([]string, 0, len(response.Messages))
	for _, message := range response.Messages {
		ids = append(ids, message.ID)
	}
	page, err := provider.loadMessages(ctx, ids)
	if err != nil {
		return mail.ChangePage{}, err
	}
	page.NextCursor = mail.SyncCursor{Kind: "google_backfill", Value: []byte(response.NextPageToken)}
	page.HasMore = response.NextPageToken != ""
	return page, nil
}

func (provider *Provider) Apply(ctx context.Context, action mail.RemoteAction) error {
	add, remove, ok := actionLabels(action.Kind, action.LabelIDs)
	if !ok || len(action.TargetIDs) == 0 || (action.TargetKind != "message" && action.TargetKind != "thread") {
		return &ProviderError{Kind: ErrorPermanent}
	}
	body, _ := json.Marshal(map[string][]string{"addLabelIds": add, "removeLabelIds": remove})
	for _, targetID := range action.TargetIDs {
		if targetID == "" {
			return &ProviderError{Kind: ErrorPermanent}
		}
		suffix := "/modify"
		if action.Kind == "move_to_trash" {
			suffix, body = "/trash", nil
		} else if action.Kind == "restore_from_trash" {
			suffix, body = "/untrash", nil
		}
		path := "/messages/" + url.PathEscape(targetID) + suffix
		if action.TargetKind == "thread" {
			path = "/threads/" + url.PathEscape(targetID) + suffix
		}
		if err := provider.json(ctx, http.MethodPost, path, nil, body, nil); err != nil {
			return err
		}
	}
	return nil
}

func (provider *Provider) SaveDraft(ctx context.Context, draft mail.OutgoingMessage) (string, error) {
	encoded, err := encodeRaw(draft.Raw)
	if err != nil {
		return "", err
	}
	message := map[string]string{"raw": encoded}
	if draft.ThreadID != "" {
		message["threadId"] = draft.ThreadID
	}
	body, _ := json.Marshal(map[string]any{"message": message})
	method, path := http.MethodPost, "/drafts"
	if draft.DraftID != "" {
		method, path = http.MethodPut, "/drafts/"+url.PathEscape(draft.DraftID)
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := provider.json(ctx, method, path, nil, body, &response); err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", &ProviderError{Kind: ErrorPermanent}
	}
	return response.ID, nil
}

func (provider *Provider) Send(ctx context.Context, message mail.OutgoingMessage) (string, error) {
	encoded, err := encodeRaw(message.Raw)
	if err != nil {
		return "", err
	}
	payload := map[string]string{"raw": encoded}
	if message.ThreadID != "" {
		payload["threadId"] = message.ThreadID
	}
	bodyPayload := any(payload)
	path := "/messages/send"
	if message.DraftID != "" {
		bodyPayload = map[string]any{"id": message.DraftID, "message": payload}
		path = "/drafts/send"
	}
	body, _ := json.Marshal(bodyPayload)
	var response struct {
		ID string `json:"id"`
	}
	if err := provider.json(ctx, http.MethodPost, path, nil, body, &response); err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", &ProviderError{Kind: ErrorPermanent}
	}
	return response.ID, nil
}

func (provider *Provider) DeleteDraft(ctx context.Context, draftID string) error {
	if strings.TrimSpace(draftID) == "" || len(draftID) > 512 {
		return &ProviderError{Kind: ErrorPermanent}
	}
	err := provider.json(ctx, http.MethodDelete, "/drafts/"+url.PathEscape(draftID), nil, nil, nil)
	var providerError *ProviderError
	if errors.As(err, &providerError) && providerError.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

func (provider *Provider) DownloadAttachment(ctx context.Context, messageID, attachmentID string) (io.ReadCloser, error) {
	if messageID == "" || attachmentID == "" {
		return nil, &ProviderError{Kind: ErrorPermanent}
	}
	if strings.HasPrefix(attachmentID, rawAttachmentPrefix) {
		index, ok := rawAttachmentIndex(attachmentID)
		if !ok {
			return nil, &ProviderError{Kind: ErrorPermanent, Reason: ReasonAttachmentMapping}
		}
		extractor, ok := provider.normalizer.(rawAttachmentExtractor)
		if !ok {
			return nil, &ProviderError{Kind: ErrorPermanent, Reason: ReasonAttachmentMapping}
		}
		var rawResponse struct {
			Raw string `json:"raw"`
		}
		if err := provider.json(ctx, http.MethodGet, "/messages/"+url.PathEscape(messageID), url.Values{"format": {"raw"}}, nil, &rawResponse); err != nil {
			return nil, err
		}
		raw, ok := decodeBase64URL(rawResponse.Raw, maxDecodedPayloadBytes)
		if !ok {
			return nil, &ProviderError{Kind: ErrorPermanent, Reason: ReasonInvalidPayload}
		}
		payload, _, err := extractor.ExtractAttachment(bytes.NewReader(raw), index)
		if err != nil {
			return nil, &ProviderError{Kind: ErrorPermanent, Reason: ReasonAttachmentMapping}
		}
		return io.NopCloser(bytes.NewReader(payload)), nil
	}
	var response struct {
		Data string `json:"data"`
	}
	if err := provider.json(ctx, http.MethodGet, "/messages/"+url.PathEscape(messageID)+"/attachments/"+url.PathEscape(attachmentID), nil, nil, &response); err != nil {
		return nil, err
	}
	decoded, ok := decodeBase64URL(response.Data, maxDecodedPayloadBytes)
	if !ok {
		return nil, &ProviderError{Kind: ErrorPermanent}
	}
	return io.NopCloser(bytes.NewReader(decoded)), nil
}
