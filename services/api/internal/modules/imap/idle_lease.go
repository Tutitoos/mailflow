package imap

import (
	"context"
	"fmt"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
)

const (
	renewWatchLeaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0`
	releaseWatchLeaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`
)

type RedisWatchLeases struct {
	client redis.UniversalClient
	prefix string
	ttl    time.Duration
}

func NewRedisWatchLeases(client redis.UniversalClient, prefix string, ttl time.Duration) (*RedisWatchLeases, error) {
	if client == nil || prefix == "" || ttl < time.Second {
		return nil, ErrWatchConfiguration
	}
	return &RedisWatchLeases{client: client, prefix: prefix, ttl: ttl}, nil
}

func (manager *RedisWatchLeases) Acquire(ctx context.Context, accountID string) (WatchLease, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return WatchLease{}, ErrWatchConfiguration
	}
	token, err := ids.New()
	if err != nil {
		return WatchLease{}, fmt.Errorf("create IMAP watch lease: %w", err)
	}
	created, err := manager.client.SetNX(ctx, manager.key(accountID), token, manager.ttl).Result()
	if err != nil {
		return WatchLease{}, fmt.Errorf("acquire IMAP watch lease: %w", err)
	}
	if !created {
		return WatchLease{}, ErrWatchLeaseHeld
	}
	return WatchLease{AccountID: accountID, token: token}, nil
}

func (manager *RedisWatchLeases) Renew(ctx context.Context, lease WatchLease) error {
	if !validWatchLease(lease) {
		return ErrWatchConfiguration
	}
	result, err := manager.client.Eval(ctx, renewWatchLeaseScript, []string{manager.key(lease.AccountID)}, lease.token, manager.ttl.Milliseconds()).Int64()
	if err != nil {
		return fmt.Errorf("renew IMAP watch lease: %w", err)
	}
	if result != 1 {
		return ErrWatchLeaseLost
	}
	return nil
}

func (manager *RedisWatchLeases) Release(ctx context.Context, lease WatchLease) error {
	if !validWatchLease(lease) {
		return ErrWatchConfiguration
	}
	result, err := manager.client.Eval(ctx, releaseWatchLeaseScript, []string{manager.key(lease.AccountID)}, lease.token).Int64()
	if err != nil {
		return fmt.Errorf("release IMAP watch lease: %w", err)
	}
	if result != 1 {
		return ErrWatchLeaseLost
	}
	return nil
}

func validWatchLease(lease WatchLease) bool {
	_, accountErr := uuid.Parse(lease.AccountID)
	_, tokenErr := uuid.Parse(lease.token)
	return accountErr == nil && tokenErr == nil
}

func (manager *RedisWatchLeases) key(accountID string) string {
	return manager.prefix + ":imap:watch:" + accountID
}

var _ WatchLeases = (*RedisWatchLeases)(nil)
