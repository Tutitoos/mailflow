package sync

import (
	"context"
	"errors"
	stdsync "sync"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

func TestAccountLeaseAllowsOneOwnerAndSafeRenewal(t *testing.T) {
	client, prefix := testkit.Redis(t)
	manager, err := NewLeaseManager(client, prefix, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("create lease manager: %v", err)
	}
	accountID := "0199ed3b-c950-7000-8000-000000000131"
	const contenders = 16
	var wait stdsync.WaitGroup
	results := make(chan Lease, contenders)
	errorsChannel := make(chan error, contenders)
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			lease, acquireErr := manager.Acquire(context.Background(), accountID)
			if acquireErr == nil {
				results <- lease
			} else {
				errorsChannel <- acquireErr
			}
		}()
	}
	wait.Wait()
	close(results)
	close(errorsChannel)
	var owner Lease
	owners := 0
	for lease := range results {
		owner = lease
		owners++
	}
	if owners != 1 {
		t.Fatalf("lease owners = %d, want 1", owners)
	}
	for acquireErr := range errorsChannel {
		if !errors.Is(acquireErr, ErrLeaseHeld) {
			t.Fatalf("contender error = %v", acquireErr)
		}
	}
	time.Sleep(time.Millisecond)
	renewed, err := manager.Renew(context.Background(), owner)
	if err != nil || !renewed.ExpiresAt.After(owner.ExpiresAt) {
		t.Fatalf("renew lease = %+v, %v", renewed, err)
	}
	if err := manager.Release(context.Background(), renewed); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	if _, err := manager.Acquire(context.Background(), accountID); err != nil {
		t.Fatalf("reacquire released lease: %v", err)
	}
}

func TestExpiredLeaseCannotDeleteNewOwner(t *testing.T) {
	client, prefix := testkit.Redis(t)
	manager, _ := NewLeaseManager(client, prefix, 100*time.Millisecond)
	accountID := "0199ed3b-c950-7000-8000-000000000132"
	stale, err := manager.Acquire(context.Background(), accountID)
	if err != nil {
		t.Fatalf("acquire stale lease: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	var current Lease
	for {
		current, err = manager.Acquire(context.Background(), accountID)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrLeaseHeld) || time.Now().After(deadline) {
			t.Fatalf("recover expired lease: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := manager.Release(context.Background(), stale); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale release error = %v", err)
	}
	if _, err := manager.Acquire(context.Background(), accountID); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("stale owner deleted current lease: %v", err)
	}
	if _, err := manager.Renew(context.Background(), stale); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale renew error = %v", err)
	}
	if err := manager.Release(context.Background(), current); err != nil {
		t.Fatalf("release current lease: %v", err)
	}
}
