package logs

import "testing"

func TestRedactSensitiveFields(t *testing.T) {
	result := Redact(map[string]any{
		"event": "mail.sent", "subject": "private", "Authorization": "Bearer secret",
		"nested": map[string]any{"accessToken": "hidden", "safe": "retry"},
		"error":  "failed for owner@example.test", "location": "https://example.test/file?signature=private",
		"path": "/file?signature=private", "identifier": "0199ed3b-c950-7000-8000-000000000017",
	})
	nested := result["nested"].(map[string]any)
	if result["event"] != "mail.sent" || result["subject"] != redacted || result["Authorization"] != redacted || nested["accessToken"] != redacted || nested["safe"] != "retry" || result["error"] != redacted || result["location"] != redacted || result["path"] != redacted || result["identifier"] != redacted {
		t.Fatalf("unexpected redaction: %#v", result)
	}
}
