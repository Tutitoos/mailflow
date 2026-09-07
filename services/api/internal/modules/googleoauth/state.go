package googleoauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type RedisStateStore struct {
	client redis.UniversalClient
	prefix string
}

func NewRedisStateStore(client redis.UniversalClient, prefix string) *RedisStateStore {
	if prefix == "" {
		prefix = "mailflow"
	}
	return &RedisStateStore{client: client, prefix: prefix}
}

func (store *RedisStateStore) Put(ctx context.Context, state string, transaction Transaction, ttl time.Duration) error {
	if state == "" || transaction.UserID == "" || transaction.CodeVerifier == "" || ttl <= 0 {
		return ErrInvalidState
	}
	encoded, err := json.Marshal(transaction)
	if err != nil {
		return err
	}
	return store.client.Set(ctx, store.key(state), encoded, ttl).Err()
}

func (store *RedisStateStore) Consume(ctx context.Context, state string) (Transaction, error) {
	if state == "" {
		return Transaction{}, ErrInvalidState
	}
	encoded, err := store.client.GetDel(ctx, store.key(state)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Transaction{}, ErrInvalidState
	}
	if err != nil {
		return Transaction{}, err
	}
	var transaction Transaction
	if json.Unmarshal(encoded, &transaction) != nil || transaction.UserID == "" || transaction.CodeVerifier == "" {
		return Transaction{}, ErrInvalidState
	}
	return transaction, nil
}

func (store *RedisStateStore) key(state string) string {
	digest := sha256.Sum256([]byte(state))
	return store.prefix + ":oauth:google:" + hex.EncodeToString(digest[:])
}
