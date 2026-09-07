package sentry

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"
)

var (
	traceIDPattern   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spanIDPattern    = regexp.MustCompile(`^[0-9a-f]{16}$`)
	operationPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

type rawTrace struct {
	TraceID      string `json:"trace_id"`
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id"`
	Operation    string `json:"op"`
	Status       string `json:"status"`
}

type rawSpan struct {
	TraceID        string  `json:"trace_id"`
	SpanID         string  `json:"span_id"`
	ParentSpanID   string  `json:"parent_span_id"`
	Operation      string  `json:"op"`
	Status         string  `json:"status"`
	StartTimestamp float64 `json:"start_timestamp"`
	Timestamp      float64 `json:"timestamp"`
}

type normalizedTrace struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Operation    string
	Status       string
	StartedAt    time.Time
	DurationMS   float64
	Spans        []normalizedSpan
}

type normalizedSpan struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Operation    string
	Status       string
	StartedAt    time.Time
	DurationMS   float64
}

type normalizedProfile struct {
	Platform    string
	SampleCount int
	FrameCount  int
}

func applyTelemetry(parsed *parsedEnvelope, metadata eventMetadata) {
	if parsed.trace == nil {
		trace := normalizeTrace(metadata.Contexts.Trace, metadata.StartTimestamp, metadata.Timestamp, metadata.Spans)
		if trace != nil {
			parsed.trace = trace
		}
	}
	if parsed.profile == nil {
		samples, frames := metadata.Samples, metadata.Frames
		if len(metadata.Profile.Samples) > 0 || len(metadata.Profile.Frames) > 0 {
			samples, frames = metadata.Profile.Samples, metadata.Profile.Frames
		}
		if len(samples) > 0 || len(frames) > 0 {
			parsed.profile = &normalizedProfile{Platform: safePlatform(metadata.Platform), SampleCount: min(len(samples), 1_000_000), FrameCount: min(len(frames), 1_000_000)}
		}
	}
	if parsed.replayID == "" {
		parsed.replayID = normalizeHexID(metadata.ReplayID, traceIDPattern)
		if metadata.SegmentID >= 0 && metadata.SegmentID <= 1_000_000 {
			parsed.replaySequence = metadata.SegmentID
		}
	}
}

func normalizeTrace(root rawTrace, started, finished float64, spans []rawSpan) *normalizedTrace {
	traceID := normalizeHexID(root.TraceID, traceIDPattern)
	spanID := normalizeHexID(root.SpanID, spanIDPattern)
	if traceID == "" || spanID == "" {
		return nil
	}
	trace := &normalizedTrace{
		TraceID: traceID, SpanID: spanID, ParentSpanID: normalizeHexID(root.ParentSpanID, spanIDPattern),
		Operation: safeOperation(root.Operation), Status: safeSpanStatus(root.Status),
		StartedAt: safeUnixTimestamp(started), DurationMS: safeDuration(started, finished),
	}
	for _, span := range spans {
		if len(trace.Spans) >= 1000 {
			break
		}
		normalized := normalizedSpan{
			TraceID: normalizeHexID(span.TraceID, traceIDPattern), SpanID: normalizeHexID(span.SpanID, spanIDPattern),
			ParentSpanID: normalizeHexID(span.ParentSpanID, spanIDPattern), Operation: safeOperation(span.Operation),
			Status: safeSpanStatus(span.Status), StartedAt: safeUnixTimestamp(span.StartTimestamp),
			DurationMS: safeDuration(span.StartTimestamp, span.Timestamp),
		}
		if normalized.TraceID == traceID && normalized.SpanID != "" {
			trace.Spans = append(trace.Spans, normalized)
		}
	}
	return trace
}

func normalizeHexID(value string, pattern *regexp.Regexp) string {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	if pattern.MatchString(value) {
		return value
	}
	return ""
}

func safeOperation(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if operationPattern.MatchString(value) {
		return value
	}
	return ""
}

func safeSpanStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "ok", "cancelled", "unknown", "invalid_argument", "deadline_exceeded", "not_found", "already_exists", "permission_denied", "resource_exhausted", "failed_precondition", "aborted", "out_of_range", "unimplemented", "internal_error", "unavailable", "data_loss", "unauthenticated":
		return value
	default:
		return ""
	}
}

func safeUnixTimestamp(value float64) time.Time {
	seconds := int64(value)
	nanos := int64((value - float64(seconds)) * float64(time.Second))
	result := time.Unix(seconds, nanos).UTC()
	if result.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || result.After(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)) {
		return time.Time{}
	}
	return result
}

func safeDuration(started, finished float64) float64 {
	if started <= 0 || finished < started || finished-started > 24*60*60 {
		return 0
	}
	return (finished - started) * 1000
}

func safePlatform(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "javascript", "node", "go", "cocoa", "rust", "native":
		return value
	default:
		return ""
	}
}

func parseReplayRecording(contentType string, payload []byte) parsedItem {
	discarded := func(code string) parsedItem {
		return parsedItem{typeName: "replay_recording", contentType: safeContentType(contentType), payload: payload, summary: mustJSON(map[string]any{"type": "replay_recording", "bytes": len(payload), "sha256": digest(payload), "discarded": true, "reason": code}), discarded: true}
	}
	if len(payload) == 0 || len(payload) > MaxReplayItemBytes {
		return discarded("size")
	}
	decoded, ok := decompressReplay(payload)
	if !ok || len(decoded) == 0 || len(decoded) > MaxReplayItemBytes {
		return discarded("encoding")
	}
	var recording any
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.UseNumber()
	if decoder.Decode(&recording) != nil {
		return discarded("json")
	}
	masked, ok := maskReplayValue(recording, 0)
	if !ok {
		return discarded("shape")
	}
	normalized, err := json.Marshal(masked)
	if err != nil || len(normalized) > MaxReplayItemBytes {
		return discarded("normalized_size")
	}
	return parsedItem{
		typeName: "replay_recording", contentType: "application/json", payload: normalized,
		summary:    mustJSON(map[string]any{"type": "replay_recording", "bytes": len(normalized), "sha256": digest(normalized), "masked": true}),
		forceStore: true,
	}
}

func decompressReplay(payload []byte) ([]byte, bool) {
	var reader io.ReadCloser
	var err error
	if len(payload) >= 2 && payload[0] == 0x1f && payload[1] == 0x8b {
		reader, err = gzip.NewReader(bytes.NewReader(payload))
	} else if len(payload) >= 2 && payload[0] == 0x78 {
		reader, err = zlib.NewReader(bytes.NewReader(payload))
	} else {
		return payload, true
	}
	if err != nil {
		return nil, false
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, MaxReplayItemBytes+1))
	return decoded, err == nil && len(decoded) <= MaxReplayItemBytes
}

func maskReplayValue(value any, depth int) (any, bool) {
	if depth > 32 {
		return nil, false
	}
	switch typed := value.(type) {
	case string:
		return "[Masked]", true
	case json.Number, float64, bool, nil:
		return typed, true
	case []any:
		if len(typed) > 10000 {
			return nil, false
		}
		result := make([]any, len(typed))
		for index, item := range typed {
			masked, ok := maskReplayValue(item, depth+1)
			if !ok {
				return nil, false
			}
			result[index] = masked
		}
		return result, true
	case map[string]any:
		if len(typed) > 1000 {
			return nil, false
		}
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if len(key) > 80 || sensitiveMetadataPattern.MatchString(key) {
				continue
			}
			masked, ok := maskReplayValue(item, depth+1)
			if !ok {
				return nil, false
			}
			result[key] = masked
		}
		return result, true
	default:
		return nil, false
	}
}

func sampled(id, domain string, rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	value := digest([]byte(domain + "\x00" + id))
	prefix := value[:8]
	var bucket uint64
	for _, character := range prefix {
		bucket <<= 4
		switch {
		case character >= '0' && character <= '9':
			bucket += uint64(character - '0')
		case character >= 'a' && character <= 'f':
			bucket += uint64(character-'a') + 10
		}
	}
	return float64(bucket)/float64(uint64(1)<<32) < rate
}
