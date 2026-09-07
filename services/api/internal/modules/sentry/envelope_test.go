package sentry

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestEnvelopeSupportsFoundationItemTypes(t *testing.T) {
	types := []string{"event", "transaction", "session", "sessions", "attachment", "client_report", "check_in"}
	for index, itemType := range types {
		payload := []byte(`{}`)
		if itemType == "attachment" {
			payload = []byte{0, 1, 2}
		}
		eventID := fmt.Sprintf("%032x", index+1)
		body := []byte(fmt.Sprintf("{\"event_id\":%q}\n{\"type\":%q,\"length\":%d}\n%s", eventID, itemType, len(payload), payload))
		parsed, err := parseEnvelope(body)
		if err != nil || parsed.eventType != itemType || len(parsed.items) != 1 {
			t.Fatalf("%s parsed=%+v err=%v", itemType, parsed, err)
		}
	}
	unsupported := []byte("{}\n{\"type\":\"unknown_item\",\"length\":2}\n{}")
	if _, err := parseEnvelope(unsupported); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("unsupported err=%v", err)
	}
}

func TestAuthenticationKeyAcceptsSDKHeaderAndBrowserQuery(t *testing.T) {
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if authenticationKey("Sentry sentry_version=7, sentry_key="+key+", sentry_client=test", "") != key {
		t.Fatal("SDK authentication header was rejected")
	}
	if authenticationKey("", key) != key {
		t.Fatal("browser query key was rejected")
	}
	if authenticationKey("Sentry sentry_key=owner@example.test", "") != "" {
		t.Fatal("unsafe project key was accepted")
	}
}

func TestDerivedProjectsSeparatePublicAndArtifactCredentials(t *testing.T) {
	projects, err := DerivedProjects([]byte("01234567890123456789012345678901"))
	if err != nil || len(projects) != 4 {
		t.Fatalf("projects=%+v err=%v", projects, err)
	}
	seen := make(map[string]bool)
	for _, project := range projects {
		if !eventIDPattern.MatchString(project.PublicKey) || !artifactTokenPattern.MatchString(project.ArtifactToken) || strings.Contains(project.ArtifactToken, project.PublicKey) {
			t.Fatalf("invalid separated credentials for %s", project.Component)
		}
		if seen[project.PublicKey] || seen[project.ArtifactToken] {
			t.Fatalf("credential reused for %s", project.Component)
		}
		seen[project.PublicKey] = true
		seen[project.ArtifactToken] = true
	}
}

func TestTelemetryNormalizationMasksReplayAndToleratesBrokenChunks(t *testing.T) {
	transaction := []byte(`{"event_id":"11111111111111111111111111111111","type":"transaction","platform":"javascript","start_timestamp":1700000000,"timestamp":1700000000.25,"contexts":{"trace":{"trace_id":"22222222222222222222222222222222","span_id":"3333333333333333","op":"ui.load","status":"ok"}},"spans":[{"trace_id":"22222222222222222222222222222222","span_id":"4444444444444444","parent_span_id":"3333333333333333","op":"http.client","status":"ok","start_timestamp":1700000000,"timestamp":1700000000.1}],"profile":{"samples":[{}],"frames":[{},{}]}}`)
	parsed, err := parseEnvelope([]byte(fmt.Sprintf("{}\n{\"type\":\"transaction\",\"length\":%d}\n%s", len(transaction), transaction)))
	if err != nil || parsed.trace == nil || len(parsed.trace.Spans) != 1 || parsed.profile == nil || parsed.profile.FrameCount != 2 {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}

	recording := []byte(`[{"type":2,"data":{"text":"private subject","email":"owner@example.test","image":"data:image/png;base64,private"}}]`)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write(recording)
	_ = writer.Close()
	item := parseReplayRecording("application/octet-stream", compressed.Bytes())
	if item.discarded || !item.forceStore || bytes.Contains(item.payload, []byte("private subject")) || bytes.Contains(item.payload, []byte("owner@example.test")) || bytes.Count(item.payload, []byte("[Masked]")) < 3 {
		t.Fatalf("unsafe replay item: %+v payload=%s", item, item.payload)
	}
	broken := parseReplayRecording("application/octet-stream", []byte{0x1f, 0x8b, 0x00})
	if !broken.discarded || broken.forceStore {
		t.Fatalf("broken Replay chunk was not isolated: %+v", broken)
	}
}

func TestTelemetrySamplingIsDeterministicAndBounded(t *testing.T) {
	const eventID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first := sampled(eventID, "trace", 0.25)
	for range 100 {
		if sampled(eventID, "trace", 0.25) != first {
			t.Fatal("sampling decision changed for the same event and domain")
		}
	}
	if sampled(eventID, "trace", 0) || !sampled(eventID, "trace", 1) {
		t.Fatal("sampling boundaries were not honored")
	}
}

func FuzzEnvelopeParserNeverPanics(f *testing.F) {
	f.Add([]byte("{}\n{\"type\":\"event\"}\n{}"))
	f.Fuzz(func(t *testing.T, payload []byte) {
		_, _ = parseEnvelope(payload)
	})
}
