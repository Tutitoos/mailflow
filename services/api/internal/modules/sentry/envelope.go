package sentry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"
)

type parsedEnvelope struct {
	eventID     string
	eventType   string
	environment string
	release     string
	level       string
	sdkName     string
	items       []parsedItem
}

type parsedItem struct {
	typeName    string
	contentType string
	payload     []byte
	summary     []byte
	discarded   bool
}

type envelopeHeader struct {
	EventID string `json:"event_id"`
}

type itemHeader struct {
	Type        string `json:"type"`
	Length      *int   `json:"length"`
	ContentType string `json:"content_type"`
	Filename    string `json:"filename"`
}

type eventMetadata struct {
	EventID     string `json:"event_id"`
	Type        string `json:"type"`
	Environment string `json:"environment"`
	Release     string `json:"release"`
	Level       string `json:"level"`
	Platform    string `json:"platform"`
	SDK         struct {
		Name string `json:"name"`
	} `json:"sdk"`
	Exception struct {
		Values []json.RawMessage `json:"values"`
	} `json:"exception"`
	Breadcrumbs struct {
		Values []json.RawMessage `json:"values"`
	} `json:"breadcrumbs"`
	Spans []json.RawMessage `json:"spans"`
}

var sensitiveMetadataPattern = regexp.MustCompile(`(?i)([a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}|[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})`)

var (
	releasePattern = regexp.MustCompile(`^(?:v?\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]+)?|[0-9a-f]{7,64})$`)
	sdkPattern     = regexp.MustCompile(`^sentry\.[a-z0-9._-]{1,120}$`)
)

func parseRequest(request Request) (parsedEnvelope, error) {
	if request.Legacy {
		return parseLegacy(request.LegacyEventID, request.Body)
	}
	return parseEnvelope(request.Body)
}

func parseLegacy(headerID string, body []byte) (parsedEnvelope, error) {
	item, metadata, err := parseJSONItem("event", "application/json", body)
	if err != nil {
		return parsedEnvelope{}, err
	}
	eventID := normalizeEventID(metadata.EventID)
	if eventID == "" {
		eventID = normalizeEventID(headerID)
	}
	if eventID == "" {
		eventID, err = newEventID()
		if err != nil {
			return parsedEnvelope{}, fmt.Errorf("create sentry event ID: %w", err)
		}
	}
	return envelopeFromMetadata(eventID, item, metadata), nil
}

func parseEnvelope(body []byte) (parsedEnvelope, error) {
	reader := bufio.NewReaderSize(bytes.NewReader(body), 64<<10)
	headerLine, err := readProtocolLine(reader)
	if err != nil {
		return parsedEnvelope{}, ErrInvalidEnvelope
	}
	var header envelopeHeader
	if json.Unmarshal(headerLine, &header) != nil {
		return parsedEnvelope{}, ErrInvalidEnvelope
	}
	parsed := parsedEnvelope{eventID: normalizeEventID(header.EventID)}
	for {
		line, lineErr := readProtocolLine(reader)
		if errors.Is(lineErr, io.EOF) && len(line) == 0 {
			break
		}
		if lineErr != nil || len(line) == 0 || len(parsed.items) >= maxEnvelopeItems {
			return parsedEnvelope{}, ErrInvalidEnvelope
		}
		var header itemHeader
		if json.Unmarshal(line, &header) != nil || !supportedItems[header.Type] {
			return parsedEnvelope{}, ErrInvalidEnvelope
		}
		payload, payloadErr := readItemPayload(reader, header.Length)
		if payloadErr != nil {
			return parsedEnvelope{}, ErrInvalidEnvelope
		}
		item, metadata, parseErr := parseItem(header, payload)
		if parseErr != nil {
			return parsedEnvelope{}, parseErr
		}
		parsed.items = append(parsed.items, item)
		if parsed.eventType == "" || header.Type == "event" || header.Type == "transaction" {
			parsed.eventType = header.Type
		}
		applyMetadata(&parsed, metadata)
	}
	if len(parsed.items) == 0 {
		return parsedEnvelope{}, ErrInvalidEnvelope
	}
	if parsed.eventID == "" {
		parsed.eventID, err = newEventID()
		if err != nil {
			return parsedEnvelope{}, fmt.Errorf("create sentry event ID: %w", err)
		}
	}
	return parsed, nil
}

func readProtocolLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	line = bytes.TrimSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) > 64<<10 {
		return nil, ErrInvalidEnvelope
	}
	return line, err
}

func readItemPayload(reader *bufio.Reader, length *int) ([]byte, error) {
	if length == nil {
		payload, err := readProtocolLine(reader)
		if errors.Is(err, io.EOF) && len(payload) > 0 {
			return payload, nil
		}
		return payload, err
	}
	if *length < 0 {
		return nil, ErrInvalidEnvelope
	}
	payload := make([]byte, *length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	if next, err := reader.Peek(1); err == nil && len(next) == 1 && next[0] == '\n' {
		_, _ = reader.ReadByte()
	}
	return payload, nil
}

func parseItem(header itemHeader, payload []byte) (parsedItem, eventMetadata, error) {
	if header.Type == "attachment" {
		summary := map[string]any{"type": "attachment", "bytes": len(payload), "sha256": digest(payload), "discarded": true}
		return parsedItem{typeName: header.Type, contentType: safeContentType(header.ContentType), payload: payload, summary: mustJSON(summary), discarded: true}, eventMetadata{}, nil
	}
	return parseJSONItem(header.Type, header.ContentType, payload)
}

func parseJSONItem(typeName, contentType string, payload []byte) (parsedItem, eventMetadata, error) {
	if !utf8.Valid(payload) || !json.Valid(payload) {
		return parsedItem{}, eventMetadata{}, ErrInvalidEnvelope
	}
	var metadata eventMetadata
	if err := json.Unmarshal(payload, &metadata); err != nil {
		return parsedItem{}, eventMetadata{}, ErrInvalidEnvelope
	}
	summary := map[string]any{
		"type": typeName, "bytes": len(payload), "sha256": digest(payload),
		"exceptionCount": len(metadata.Exception.Values), "breadcrumbCount": len(metadata.Breadcrumbs.Values), "spanCount": len(metadata.Spans),
	}
	return parsedItem{typeName: typeName, contentType: safeContentType(contentType), payload: payload, summary: mustJSON(summary)}, metadata, nil
}

func envelopeFromMetadata(eventID string, item parsedItem, metadata eventMetadata) parsedEnvelope {
	parsed := parsedEnvelope{eventID: eventID, eventType: item.typeName, items: []parsedItem{item}}
	applyMetadata(&parsed, metadata)
	return parsed
}

func applyMetadata(parsed *parsedEnvelope, metadata eventMetadata) {
	if parsed.eventID == "" {
		parsed.eventID = normalizeEventID(metadata.EventID)
	}
	if parsed.environment == "" {
		parsed.environment = safeEnvironment(metadata.Environment)
	}
	if parsed.release == "" {
		parsed.release = safeRelease(metadata.Release)
	}
	if parsed.level == "" {
		parsed.level = safeLevel(metadata.Level)
	}
	if parsed.sdkName == "" {
		parsed.sdkName = safeSDKName(metadata.SDK.Name)
	}
}

func normalizeEventID(value string) string {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	if !eventIDPattern.MatchString(value) {
		return ""
	}
	return value
}

func safeEnvironment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "production", "staging", "development", "test":
		return value
	default:
		return ""
	}
}

func safeRelease(value string) string {
	value = strings.TrimSpace(value)
	if releasePattern.MatchString(value) && !sensitiveMetadataPattern.MatchString(value) {
		return value
	}
	return ""
}

func safeSDKName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if sdkPattern.MatchString(value) {
		return value
	}
	return ""
}

func safeLevel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "debug", "info", "warning", "error", "fatal":
		return value
	default:
		return ""
	}
}

func safeContentType(value string) string {
	value, _, err := mime.ParseMediaType(strings.ToLower(strings.TrimSpace(value)))
	if err != nil || len(value) > 128 {
		return ""
	}
	return value
}

func mustJSON(value any) []byte {
	payload, _ := json.Marshal(value)
	return payload
}
