package microsoftgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 32 << 20

type graphErrorResponse struct {
	Error graphErrorNode `json:"error"`
}

type graphErrorNode struct {
	Code       string          `json:"code"`
	InnerError *graphErrorNode `json:"innerError"`
}

func (provider *Provider) json(ctx context.Context, method, path string, query url.Values, body []byte, contentType string, destination any) error {
	response, err := provider.do(ctx, method, path, query, body, contentType)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if destination == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(payload) > maxResponseBytes || json.Unmarshal(payload, destination) != nil {
		return &ProviderError{Kind: ErrorPermanent, StatusCode: response.StatusCode}
	}
	return nil
}

func (provider *Provider) do(ctx context.Context, method, path string, query url.Values, body []byte, contentType string) (*http.Response, error) {
	endpoint, err := provider.endpoint(path, query)
	if err != nil {
		return nil, permanentError()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, permanentError()
	}
	request.Header.Set("Authorization", "Bearer "+provider.accessToken)
	request.Header.Set("Prefer", `IdType="ImmutableId"`)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := provider.http.Do(request)
	if err != nil {
		return nil, &ProviderError{Kind: ErrorTransient}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, classify(response.StatusCode, response.Header.Get("Retry-After"), payload)
	}
	return response, nil
}

func (provider *Provider) endpoint(path string, query url.Values) (string, error) {
	base := strings.TrimRight(provider.baseURL.String(), "/")
	endpointValue := base
	if path != "" {
		basePath := strings.TrimRight(provider.baseURL.EscapedPath(), "/")
		switch {
		case strings.HasPrefix(path, basePath+"/"):
			endpointValue = provider.baseURL.Scheme + "://" + provider.baseURL.Host + path
		case strings.HasPrefix(path, "/"):
			endpointValue = base + path
		default:
			return "", ErrInvalidCursor
		}
	}
	endpoint, err := url.Parse(endpointValue)
	basePath := strings.TrimRight(provider.baseURL.Path, "/")
	if err != nil || endpoint.User != nil || endpoint.Fragment != "" || !strings.EqualFold(endpoint.Scheme, provider.baseURL.Scheme) || !strings.EqualFold(endpoint.Host, provider.baseURL.Host) || (endpoint.Path != basePath && !strings.HasPrefix(endpoint.Path, basePath+"/")) {
		return "", ErrInvalidCursor
	}
	if len(query) > 0 {
		endpoint.RawQuery = query.Encode()
	}
	return endpoint.String(), nil
}

func (provider *Provider) relativeNextLink(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > maxCursorBytes-256 {
		return "", ErrInvalidCursor
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return "", ErrInvalidCursor
	}
	if parsed.IsAbs() {
		if !strings.EqualFold(parsed.Scheme, provider.baseURL.Scheme) || !strings.EqualFold(parsed.Host, provider.baseURL.Host) {
			return "", ErrInvalidCursor
		}
	}
	prefix := strings.TrimRight(provider.baseURL.Path, "/") + "/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return "", ErrInvalidCursor
	}
	return parsed.EscapedPath() + querySuffix(parsed.RawQuery), nil
}

func querySuffix(query string) string {
	if query == "" {
		return ""
	}
	return "?" + query
}

func classify(status int, retryAfter string, payload []byte) error {
	response := graphErrorResponse{}
	_ = json.Unmarshal(payload, &response)
	code := ""
	for node, depth := &response.Error, 0; node != nil && depth < 8; node, depth = node.InnerError, depth+1 {
		if candidate := safeErrorCode(node.Code); candidate != "" {
			code = candidate
		}
	}
	kind := ErrorPermanent
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden || code == "invalidauthenticationtoken" || code == "erroraccessdenied":
		kind = ErrorAuthorization
	case status == http.StatusTooManyRequests || status == 509 || code == "toomanyrequests" || code == "errorquotaexceeded" || code == "activitylimitreached":
		kind = ErrorQuota
	case status == http.StatusRequestTimeout || status >= 500 || code == "errorserverbusy" || code == "mailboxconcurrency" || code == "errorinternalservertransienterror" || code == "timeout":
		kind = ErrorTransient
	}
	return &ProviderError{Kind: kind, StatusCode: status, Code: code, RetryAfter: parseRetryAfter(retryAfter)}
}

func safeErrorCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 128 {
		return ""
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' {
			return ""
		}
	}
	return value
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
