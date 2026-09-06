package ids

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewReturnsUUIDv7(t *testing.T) {
	value, err := New()
	if err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(value)
	if err != nil || id.Version() != 7 {
		t.Fatalf("expected UUIDv7, got %q: %v", value, err)
	}
}
