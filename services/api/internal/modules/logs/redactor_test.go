package logs

import "testing"

func TestRedactSensitiveFields(t *testing.T) {
	result := Redact(map[string]any{"event": "mail.sent", "subject": "private", "Authorization": "Bearer secret"})
	if result["event"] != "mail.sent" || result["subject"] != "[REDACTED]" || result["Authorization"] != "[REDACTED]" {
		t.Fatalf("unexpected redaction: %#v", result)
	}
}
