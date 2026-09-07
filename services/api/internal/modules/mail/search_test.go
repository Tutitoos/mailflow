package mail

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSearchOperatorsAndText(t *testing.T) {
	query, err := ParseSearch(`quarterly report from:alex@example.test subject:"Project Atlas" after:2026-01-01 before:2026-02-01 has:attachment is:unread is:starred label:work in:inbox`)
	if err != nil {
		t.Fatal(err)
	}
	if query.Text != "quarterly report" || query.From[0] != "alex@example.test" || query.Subjects[0] != "Project Atlas" || query.After == nil || query.Before == nil || !query.HasAttachment || !query.Unread || !query.Starred || query.Labels[0] != "work" || query.Mailboxes[0] != "inbox" {
		t.Fatalf("unexpected query: %#v", query)
	}
}

func TestParseSearchReturnsStableValidationCodes(t *testing.T) {
	tests := map[string]string{
		"from:":                              "missing_value",
		"unknown:value":                      "unsupported_operator",
		`subject:"missing`:                   "unclosed_quote",
		"after:yesterday":                    "invalid_value",
		"after:2026-02-01 before:2026-01-01": "invalid_range",
		"has:image":                          "invalid_value",
		"is:important":                       "invalid_value",
		"":                                   "empty_query",
	}
	for input, code := range tests {
		_, err := ParseSearch(input)
		var validation *SearchValidationError
		if !errors.As(err, &validation) || validation.Code != code {
			t.Errorf("ParseSearch(%q) error = %v, want %s", input, err, code)
		}
		if err != nil && input != "" && strings.Contains(err.Error(), input) {
			t.Errorf("validation error exposed query input: %v", err)
		}
	}
}
