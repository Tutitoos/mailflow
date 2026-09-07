package sync

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

func TestRedisActivityTrackerUsesExpiringOpaqueAccountKey(t *testing.T) {
	client, prefix := testkit.Redis(t)
	tracker := NewRedisActivityTracker(client, prefix, time.Minute)
	account := "0199ed3b-c950-7000-8000-000000000016"
	if tracker.Active(context.Background(), "user", account) {
		t.Fatal("account unexpectedly active")
	}
	if err := tracker.Touch(context.Background(), "user", account); err != nil || !tracker.Active(context.Background(), "user", account) {
		t.Fatalf("touch error=%v active=%v", err, tracker.Active(context.Background(), "user", account))
	}
	keys, err := client.Keys(context.Background(), prefix+":sync:activity:*").Result()
	if err != nil || len(keys) != 1 || strings.Contains(keys[0], account) {
		t.Fatalf("activity keys=%v error=%v", keys, err)
	}
}
