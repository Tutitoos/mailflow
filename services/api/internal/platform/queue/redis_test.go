package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

func TestRedisQueueLifecycle(t *testing.T) {
	client, prefix := testkit.Redis(t)
	config := DefaultConfig()
	config.Prefix = prefix
	config.Consumer = "worker-one"
	config.ClaimTimeout = 25 * time.Millisecond
	config.ReadBlock = 10 * time.Millisecond
	config.BaseBackoff = time.Millisecond
	config.MaxBackoff = 2 * time.Millisecond
	store, err := NewRedisStore(context.Background(), client, config)
	if err != nil {
		t.Fatal(err)
	}

	job, created, err := store.Enqueue(context.Background(), "test.deliver", json.RawMessage(`{"message":"safe fixture"}`), EnqueueOptions{IdempotencyKey: "same-request", MaxAttempts: 2})
	if err != nil || !created {
		t.Fatalf("enqueue failed: created=%v err=%v", created, err)
	}
	assertStats(t, store, Stats{Ready: 1})
	duplicate, created, err := store.Enqueue(context.Background(), "test.deliver", json.RawMessage(`{"message":"ignored duplicate"}`), EnqueueOptions{IdempotencyKey: "same-request", MaxAttempts: 2})
	if err != nil || created || duplicate.ID != job.ID {
		t.Fatalf("idempotent enqueue failed: first=%s duplicate=%s created=%v err=%v", job.ID, duplicate.ID, created, err)
	}

	claimed, err := store.Claim(context.Background())
	if err != nil || claimed.ID != job.ID {
		t.Fatalf("claim failed: job=%+v err=%v", claimed, err)
	}
	assertStats(t, store, Stats{Pending: 1})
	if err := store.Acknowledge(context.Background(), claimed); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background()); !errors.Is(err, ErrNoJob) {
		t.Fatalf("acknowledged job became available again: %v", err)
	}
	assertStats(t, store, Stats{})

	retryJob, _, err := store.Enqueue(context.Background(), "test.retry", json.RawMessage(`{}`), EnqueueOptions{MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	firstAttempt, err := store.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dead, err := store.Retry(context.Background(), firstAttempt, errors.New("provider response must not be retained"))
	if err != nil || dead {
		t.Fatalf("first failure should retry: dead=%v err=%v", dead, err)
	}
	assertStats(t, store, Stats{Retry: 1})
	time.Sleep(3 * time.Millisecond)
	secondAttempt, err := store.Claim(context.Background())
	if err != nil || secondAttempt.ID != retryJob.ID || secondAttempt.Attempt != 1 {
		t.Fatalf("retry claim failed: job=%+v err=%v", secondAttempt, err)
	}
	dead, err = store.Retry(context.Background(), secondAttempt, errors.New("sensitive provider response"))
	if err != nil || !dead {
		t.Fatalf("exhausted job should dead-letter: dead=%v err=%v", dead, err)
	}
	deadLetters, err := store.DeadLetters(context.Background(), 10)
	if err != nil || len(deadLetters) != 1 || deadLetters[0].ID != retryJob.ID || deadLetters[0].Error != "job_failed" {
		t.Fatalf("unexpected dead letters: jobs=%+v err=%v", deadLetters, err)
	}
	assertStats(t, store, Stats{Dead: 1})
}

type delayedRetryError struct{ delay time.Duration }

func (failure delayedRetryError) Error() string             { return "provider retry requested" }
func (failure delayedRetryError) RetryDelay() time.Duration { return failure.delay }

func TestRedisQueueHonorsBoundedProviderRetryDelay(t *testing.T) {
	client, prefix := testkit.Redis(t)
	config := DefaultConfig()
	config.Prefix, config.Consumer = prefix, "rate-limited-worker"
	config.ReadBlock, config.BaseBackoff, config.MaxBackoff = 5*time.Millisecond, time.Millisecond, 80*time.Millisecond
	store, err := NewRedisStore(context.Background(), client, config)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := store.Enqueue(context.Background(), "test.rate_limit", json.RawMessage(`{}`), EnqueueOptions{MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if dead, err := store.Retry(context.Background(), claimed, delayedRetryError{delay: 50 * time.Millisecond}); err != nil || dead {
		t.Fatalf("delayed retry dead=%v error=%v", dead, err)
	}
	if _, err := store.Claim(context.Background()); !errors.Is(err, ErrNoJob) {
		t.Fatalf("rate-limited job was promoted early: %v", err)
	}
	time.Sleep(55 * time.Millisecond)
	retried, err := store.Claim(context.Background())
	if err != nil || retried.ID != job.ID || retried.Attempt != 1 {
		t.Fatalf("delayed job=%+v error=%v", retried, err)
	}
}

func assertStats(t *testing.T, store *RedisStore, want Stats) {
	t.Helper()
	got, err := store.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected queue stats %+v, got %+v", want, got)
	}
}

func TestExpiredClaimIsRecoveredByAnotherConsumer(t *testing.T) {
	client, prefix := testkit.Redis(t)
	config := DefaultConfig()
	config.Prefix, config.Consumer = prefix, "crashed-worker"
	config.ClaimTimeout, config.ReadBlock = 20*time.Millisecond, 10*time.Millisecond
	first, err := NewRedisStore(context.Background(), client, config)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := first.Enqueue(context.Background(), "test.recover", json.RawMessage(`{}`), EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	config.Consumer = "replacement-worker"
	replacement, err := NewRedisStore(context.Background(), client, config)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := replacement.Claim(context.Background())
	if err != nil || recovered.ID != job.ID {
		t.Fatalf("expired claim was not recovered: job=%+v err=%v", recovered, err)
	}
}

func TestQueueRejectsUnboundedOrMalformedJobs(t *testing.T) {
	config := DefaultConfig()
	config.MaxPayloadBytes = 2
	store := &RedisStore{config: config}
	for _, test := range []struct {
		kind    string
		payload json.RawMessage
		options EnqueueOptions
	}{
		{kind: "Invalid Kind", payload: json.RawMessage(`{}`)},
		{kind: "valid", payload: json.RawMessage(`not-json`)},
		{kind: "valid", payload: json.RawMessage(`{"too":"large"}`)},
		{kind: "valid", payload: json.RawMessage(`{}`), options: EnqueueOptions{IdempotencyKey: string(make([]byte, 513))}},
	} {
		if _, _, err := store.Enqueue(context.Background(), test.kind, test.payload, test.options); err == nil {
			t.Fatalf("expected invalid job to be rejected: %+v", test)
		}
	}
}
