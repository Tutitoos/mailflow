package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	redis "github.com/redis/go-redis/v9"
)

const DefaultActivityTTL = 15 * time.Minute

type ActivityMarker interface {
	ActivityTracker
	Touch(context.Context, string, string) error
}

type RedisActivityTracker struct {
	client redis.UniversalClient
	prefix string
	ttl    time.Duration
}

func NewRedisActivityTracker(client redis.UniversalClient, prefix string, ttl time.Duration) *RedisActivityTracker {
	if prefix == "" {
		prefix = "mailflow"
	}
	if ttl <= 0 {
		ttl = DefaultActivityTTL
	}
	return &RedisActivityTracker{client: client, prefix: prefix, ttl: ttl}
}

func (tracker *RedisActivityTracker) Touch(ctx context.Context, _, account string) error {
	return tracker.client.Set(ctx, tracker.key(account), "1", tracker.ttl).Err()
}

func (tracker *RedisActivityTracker) Active(ctx context.Context, _, account string) bool {
	active, err := tracker.client.Exists(ctx, tracker.key(account)).Result()
	return err == nil && active == 1
}

func (tracker *RedisActivityTracker) key(account string) string {
	digest := sha256.Sum256([]byte(account))
	return tracker.prefix + ":sync:activity:" + hex.EncodeToString(digest[:])
}
