package cdn

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestStoreWritesAtomicallyAndRejectsTraversal(t *testing.T) {
	store, err := NewStore(t.TempDir(), 16)
	if err != nil {
		t.Fatal(err)
	}
	id := "018f1f65-7c6a-7c9a-b0c4-44ab2a7f91cc"
	if _, err := store.Put("attachments", id, strings.NewReader("mailflow")); err != nil {
		t.Fatal(err)
	}
	object, err := store.Open("attachments", id)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	content, err := io.ReadAll(object)
	if err != nil || string(content) != "mailflow" {
		t.Fatalf("unexpected content %q: %v", content, err)
	}
	if _, err := store.Put("attachments", "../../secret", strings.NewReader("x")); !errors.Is(err, ErrInvalidObjectID) {
		t.Fatalf("expected invalid object id, got %v", err)
	}
	if _, err := store.Put("attachments", id, strings.NewReader("this payload is much too large")); !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("expected size rejection, got %v", err)
	}
}
