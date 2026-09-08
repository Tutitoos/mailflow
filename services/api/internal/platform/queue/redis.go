package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	redis "github.com/redis/go-redis/v9"
)

const (
	enqueueScript = `
if KEYS[2] ~= '' then
  local existing = redis.call('GET', KEYS[2])
  if existing then return {existing, '0'} end
end
redis.call('XADD', KEYS[1], '*', 'job', ARGV[1])
if KEYS[2] ~= '' then redis.call('SET', KEYS[2], ARGV[2], 'EX', ARGV[3]) end
return {ARGV[2], '1'}`
	acknowledgeScript = `
redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
redis.call('XDEL', KEYS[1], ARGV[2])
return 1`
	retryScript = `
redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
redis.call('XDEL', KEYS[1], ARGV[2])
redis.call('ZADD', KEYS[2], ARGV[3], ARGV[4])
return 1`
	deadScript = `
redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
redis.call('XDEL', KEYS[1], ARGV[2])
redis.call('XADD', KEYS[2], '*', 'job', ARGV[3])
return 1`
	promoteScript = `
local jobs = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, ARGV[2])
for _, job in ipairs(jobs) do
  if redis.call('ZREM', KEYS[1], job) == 1 then redis.call('XADD', KEYS[2], '*', 'job', job) end
end
return #jobs`
)

type RedisStore struct {
	client redis.UniversalClient
	config Config
	ready  string
	retry  string
	dead   string
	group  string
}

func NewRedisStore(ctx context.Context, client redis.UniversalClient, config Config) (*RedisStore, error) {
	defaults := DefaultConfig()
	if config.Prefix == "" {
		config.Prefix = defaults.Prefix
	}
	if config.Consumer == "" {
		return nil, errors.New("queue consumer is required")
	}
	if config.ClaimTimeout <= 0 {
		config.ClaimTimeout = defaults.ClaimTimeout
	}
	if config.ReadBlock <= 0 {
		config.ReadBlock = defaults.ReadBlock
	}
	if config.IdempotencyTTL <= 0 {
		config.IdempotencyTTL = defaults.IdempotencyTTL
	}
	if config.IdempotencyTTL < time.Second {
		return nil, errors.New("queue idempotency TTL must be at least one second")
	}
	if config.DefaultMaxAttempts <= 0 {
		config.DefaultMaxAttempts = defaults.DefaultMaxAttempts
	}
	if config.BaseBackoff <= 0 {
		config.BaseBackoff = defaults.BaseBackoff
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = defaults.MaxBackoff
	}
	if config.MaxPayloadBytes <= 0 {
		config.MaxPayloadBytes = defaults.MaxPayloadBytes
	}
	store := &RedisStore{client: client, config: config, ready: config.Prefix + ":jobs:ready", retry: config.Prefix + ":jobs:retry", dead: config.Prefix + ":jobs:dead", group: config.Prefix + "-workers"}
	if err := client.XGroupCreateMkStream(ctx, store.ready, store.group, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return nil, fmt.Errorf("create queue consumer group: %w", err)
	}
	return store, nil
}

func (store *RedisStore) Ping(ctx context.Context) error { return store.client.Ping(ctx).Err() }

func (store *RedisStore) Enqueue(ctx context.Context, kind string, payload json.RawMessage, options EnqueueOptions) (Job, bool, error) {
	if !validKind(kind) || !json.Valid(payload) {
		return Job{}, false, errors.New("job kind and valid JSON payload are required")
	}
	if len(payload) > store.config.MaxPayloadBytes {
		return Job{}, false, errors.New("job payload exceeds the configured limit")
	}
	if len(options.IdempotencyKey) > 512 {
		return Job{}, false, errors.New("job idempotency key exceeds the configured limit")
	}
	id, err := ids.New()
	if err != nil {
		return Job{}, false, fmt.Errorf("create job ID: %w", err)
	}
	maxAttempts := options.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = store.config.DefaultMaxAttempts
	}
	job := Job{Version: EnvelopeVersion, ID: id, Kind: kind, Payload: payload, MaxAttempts: maxAttempts, CreatedAt: time.Now().UTC()}
	encoded, err := json.Marshal(job)
	if err != nil {
		return Job{}, false, fmt.Errorf("encode job: %w", err)
	}
	idempotencyKey := ""
	if options.IdempotencyKey != "" {
		sum := sha256.Sum256([]byte(options.IdempotencyKey))
		idempotencyKey = store.config.Prefix + ":jobs:idempotency:" + hex.EncodeToString(sum[:])
	}
	result, err := store.client.Eval(ctx, enqueueScript, []string{store.ready, idempotencyKey}, string(encoded), id, int64(store.config.IdempotencyTTL/time.Second)).Slice()
	if err != nil {
		return Job{}, false, fmt.Errorf("enqueue job: %w", err)
	}
	created := fmt.Sprint(result[1]) == "1"
	if !created {
		job.ID = fmt.Sprint(result[0])
	}
	return job, created, nil
}

func (store *RedisStore) Claim(ctx context.Context) (ClaimedJob, error) {
	if err := store.promote(ctx); err != nil {
		return ClaimedJob{}, err
	}
	messages, _, err := store.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: store.ready, Group: store.group, Consumer: store.config.Consumer, MinIdle: store.config.ClaimTimeout, Start: "0-0", Count: 1}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return ClaimedJob{}, fmt.Errorf("reclaim expired job: %w", err)
	}
	if len(messages) > 0 {
		return decodeClaim(messages[0])
	}
	streams, err := store.client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: store.group, Consumer: store.config.Consumer, Streams: []string{store.ready, ">"}, Count: 1, Block: store.config.ReadBlock}).Result()
	if errors.Is(err, redis.Nil) {
		return ClaimedJob{}, ErrNoJob
	}
	if err != nil {
		return ClaimedJob{}, fmt.Errorf("claim job: %w", err)
	}
	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		return ClaimedJob{}, ErrNoJob
	}
	return decodeClaim(streams[0].Messages[0])
}

func (store *RedisStore) Acknowledge(ctx context.Context, job ClaimedJob) error {
	return store.evalReceipt(ctx, acknowledgeScript, []string{store.ready}, job)
}

func (store *RedisStore) Retry(ctx context.Context, claimed ClaimedJob, cause error) (bool, error) {
	claimed.Attempt++
	if claimed.Attempt >= claimed.MaxAttempts {
		dead := DeadJob{Job: claimed.Job, Error: safeError(cause), FailedAt: time.Now().UTC()}
		encoded, err := json.Marshal(dead)
		if err != nil {
			return false, err
		}
		if err := store.evalReceipt(ctx, deadScript, []string{store.ready, store.dead}, claimed, string(encoded)); err != nil {
			return false, err
		}
		return true, nil
	}
	encoded, err := json.Marshal(claimed.Job)
	if err != nil {
		return false, err
	}
	delay := store.backoff(claimed.Attempt)
	var hinted interface{ RetryDelay() time.Duration }
	if errors.As(cause, &hinted) && hinted.RetryDelay() > delay {
		delay = hinted.RetryDelay()
		if delay > store.config.MaxBackoff {
			delay = store.config.MaxBackoff
		}
	}
	due := time.Now().Add(delay).UnixMilli()
	if err := store.evalReceipt(ctx, retryScript, []string{store.ready, store.retry}, claimed, due, string(encoded)); err != nil {
		return false, err
	}
	return false, nil
}

func (store *RedisStore) Release(ctx context.Context, claimed ClaimedJob) error {
	encoded, err := json.Marshal(claimed.Job)
	if err != nil {
		return err
	}
	return store.evalReceipt(ctx, retryScript, []string{store.ready, store.retry}, claimed, time.Now().UnixMilli(), string(encoded))
}

func (store *RedisStore) DeadLetters(ctx context.Context, limit int64) ([]DeadJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	messages, err := store.client.XRevRangeN(ctx, store.dead, "+", "-", limit).Result()
	if err != nil {
		return nil, fmt.Errorf("read dead letters: %w", err)
	}
	jobs := make([]DeadJob, 0, len(messages))
	for _, message := range messages {
		encoded, ok := message.Values["job"].(string)
		if !ok {
			return nil, errors.New("dead-letter entry is malformed")
		}
		var job DeadJob
		if err := json.Unmarshal([]byte(encoded), &job); err != nil {
			return nil, fmt.Errorf("decode dead letter: %w", err)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (store *RedisStore) Stats(ctx context.Context) (Stats, error) {
	ready, err := store.client.XLen(ctx, store.ready).Result()
	if err != nil {
		return Stats{}, err
	}
	retryCount, err := store.client.ZCard(ctx, store.retry).Result()
	if err != nil {
		return Stats{}, err
	}
	dead, err := store.client.XLen(ctx, store.dead).Result()
	if err != nil {
		return Stats{}, err
	}
	groups, err := store.client.XInfoGroups(ctx, store.ready).Result()
	if err != nil {
		return Stats{}, err
	}
	var pending int64
	for _, group := range groups {
		if group.Name == store.group {
			pending = group.Pending
		}
	}
	return Stats{Ready: ready - pending, Pending: pending, Retry: retryCount, Dead: dead}, nil
}

func (store *RedisStore) promote(ctx context.Context) error {
	if err := store.client.Eval(ctx, promoteScript, []string{store.retry, store.ready}, time.Now().UnixMilli(), 100).Err(); err != nil {
		return fmt.Errorf("promote retry jobs: %w", err)
	}
	return nil
}

func (store *RedisStore) evalReceipt(ctx context.Context, script string, keys []string, job ClaimedJob, extra ...any) error {
	args := append([]any{store.group, job.Receipt}, extra...)
	if err := store.client.Eval(ctx, script, keys, args...).Err(); err != nil {
		return fmt.Errorf("update claimed job: %w", err)
	}
	return nil
}

func (store *RedisStore) backoff(attempt int) time.Duration {
	delay := store.config.BaseBackoff
	for current := 1; current < attempt && delay < store.config.MaxBackoff; current++ {
		delay *= 2
	}
	if delay > store.config.MaxBackoff {
		return store.config.MaxBackoff
	}
	return delay
}

func decodeClaim(message redis.XMessage) (ClaimedJob, error) {
	encoded, ok := message.Values["job"].(string)
	if !ok {
		return ClaimedJob{}, errors.New("queue entry is malformed")
	}
	var job Job
	if err := json.Unmarshal([]byte(encoded), &job); err != nil {
		return ClaimedJob{}, fmt.Errorf("decode job: %w", err)
	}
	if job.Version != EnvelopeVersion {
		return ClaimedJob{}, fmt.Errorf("unsupported job envelope version %d", job.Version)
	}
	return ClaimedJob{Job: job, Receipt: message.ID}, nil
}

func safeError(err error) string {
	var coded CodedError
	if !errors.As(err, &coded) {
		return "job_failed"
	}
	code := coded.JobErrorCode()
	if code == "" || len(code) > 64 {
		return "job_failed"
	}
	for _, character := range code {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return "job_failed"
		}
	}
	return code
}

func validKind(kind string) bool {
	if kind == "" || len(kind) > 64 {
		return false
	}
	for _, character := range kind {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return false
		}
	}
	return true
}
