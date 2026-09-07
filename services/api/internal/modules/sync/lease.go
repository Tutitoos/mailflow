package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
)

const (
	renewLeaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0`
	releaseLeaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`
)

var (
	ErrInvalidLease = errors.New("invalid account lease")
	ErrLeaseHeld    = errors.New("account lease is already held")
	ErrLeaseLost    = errors.New("account lease ownership was lost")
)

type Lease struct {
	AccountID string
	token     string
	ExpiresAt time.Time
}

type LeaseManager struct {
	client redis.UniversalClient
	prefix string
	ttl    time.Duration
}

func NewLeaseManager(client redis.UniversalClient, prefix string, ttl time.Duration) (*LeaseManager, error) {
	if client == nil || prefix == "" || ttl < 100*time.Millisecond {
		return nil, ErrInvalidLease
	}
	return &LeaseManager{client: client, prefix: prefix, ttl: ttl}, nil
}

func (manager *LeaseManager) Acquire(ctx context.Context, accountID string) (Lease, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return Lease{}, ErrInvalidLease
	}
	token, err := ids.New()
	if err != nil {
		return Lease{}, fmt.Errorf("create account lease token: %w", err)
	}
	created, err := manager.client.SetNX(ctx, manager.key(accountID), token, manager.ttl).Result()
	if err != nil {
		return Lease{}, fmt.Errorf("acquire account lease: %w", err)
	}
	if !created {
		return Lease{}, ErrLeaseHeld
	}
	return Lease{AccountID: accountID, token: token, ExpiresAt: time.Now().UTC().Add(manager.ttl)}, nil
}

func (manager *LeaseManager) Renew(ctx context.Context, lease Lease) (Lease, error) {
	if !manager.validLease(lease) {
		return Lease{}, ErrInvalidLease
	}
	result, err := manager.client.Eval(ctx, renewLeaseScript, []string{manager.key(lease.AccountID)}, lease.token, manager.ttl.Milliseconds()).Int64()
	if err != nil {
		return Lease{}, fmt.Errorf("renew account lease: %w", err)
	}
	if result != 1 {
		return Lease{}, ErrLeaseLost
	}
	lease.ExpiresAt = time.Now().UTC().Add(manager.ttl)
	return lease, nil
}

func (manager *LeaseManager) Release(ctx context.Context, lease Lease) error {
	if !manager.validLease(lease) {
		return ErrInvalidLease
	}
	result, err := manager.client.Eval(ctx, releaseLeaseScript, []string{manager.key(lease.AccountID)}, lease.token).Int64()
	if err != nil {
		return fmt.Errorf("release account lease: %w", err)
	}
	if result != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (manager *LeaseManager) validLease(lease Lease) bool {
	_, accountErr := uuid.Parse(lease.AccountID)
	_, tokenErr := uuid.Parse(lease.token)
	return accountErr == nil && tokenErr == nil
}

func (manager *LeaseManager) key(accountID string) string {
	return manager.prefix + ":sync:lease:" + accountID
}
