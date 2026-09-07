package cdn

import (
	"errors"
	"io"
	"os"
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

func TestStoreValidatesMediaTypeAndPreservesAtomicReplacement(t *testing.T) {
	store, err := NewStore(t.TempDir(), 16)
	if err != nil {
		t.Fatal(err)
	}
	id := "018f1f65-7c6a-7c9a-b0c4-44ab2a7f91cc"
	if _, err := store.PutValidated("attachments", id, "text/plain", strings.NewReader("original")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutValidated("attachments", id, "image/png", strings.NewReader("not an image")); !errors.Is(err, ErrMediaTypeMismatch) {
		t.Fatalf("spoofed media type error = %v", err)
	}
	if _, err := store.PutValidated("attachments", id, "text/plain", strings.NewReader("replacement exceeds limit")); !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("oversize replacement error = %v", err)
	}
	object, err := store.Open("attachments", id)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(object)
	_ = object.Close()
	if string(content) != "original" {
		t.Fatalf("failed replacement changed object to %q", content)
	}
	if err := store.Remove("attachments", id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open("attachments", id); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed object error = %v", err)
	}
}
