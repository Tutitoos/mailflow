package googleoauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

func TestRedisStateStoreConsumesStateOnce(t *testing.T) {
	client, prefix := testkit.Redis(t)
	store := NewRedisStateStore(client, prefix)
	want := Transaction{UserID: "0199ed3b-c950-7000-8000-000000000001", CodeVerifier: "verifier", Reconsent: true}
	if err := store.Put(context.Background(), "private-state", want, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := store.Consume(context.Background(), "private-state")
	if err != nil || got != want {
		t.Fatalf("consume = %+v, %v", got, err)
	}
	if _, err := store.Consume(context.Background(), "private-state"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replay error = %v", err)
	}
}
