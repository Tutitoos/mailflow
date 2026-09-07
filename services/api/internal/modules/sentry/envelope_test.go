package sentry

import (
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
	unsupported := []byte("{}\n{\"type\":\"replay_event\",\"length\":2}\n{}")
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

func FuzzEnvelopeParserNeverPanics(f *testing.F) {
	f.Add([]byte("{}\n{\"type\":\"event\"}\n{}"))
	f.Fuzz(func(t *testing.T, payload []byte) {
		_, _ = parseEnvelope(payload)
	})
}
