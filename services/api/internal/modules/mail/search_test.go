package mail

import "testing"

func TestParseSearchOperatorsAndText(t *testing.T) {
	query, err := ParseSearch(`quarterly report from:alex@example.com subject:"Project Atlas" is:unread`)
	if err != nil {
		t.Fatal(err)
	}
	if query.Text != "quarterly report" {
		t.Fatalf("unexpected text: %q", query.Text)
	}
	if query.Operators["from"][0] != "alex@example.com" || query.Operators["subject"][0] != "Project Atlas" {
		t.Fatalf("unexpected operators: %#v", query.Operators)
	}
}

func TestParseSearchRejectsEmptyOperator(t *testing.T) {
	if _, err := ParseSearch("from:"); err == nil {
		t.Fatal("expected empty operator value to fail")
	}
}
