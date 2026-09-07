package queue

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type runnerStore struct {
	mu       sync.Mutex
	job      ClaimedJob
	released bool
	acked    bool
	claimed  bool
}

func (store *runnerStore) Ping(context.Context) error { return nil }
func (store *runnerStore) Enqueue(context.Context, string, json.RawMessage, EnqueueOptions) (Job, bool, error) {
	return Job{}, false, errors.New("not implemented")
}
func (store *runnerStore) Claim(ctx context.Context) (ClaimedJob, error) {
	store.mu.Lock()
	if !store.claimed {
		store.claimed = true
		job := store.job
		store.mu.Unlock()
		return job, nil
	}
	store.mu.Unlock()
	<-ctx.Done()
	return ClaimedJob{}, ctx.Err()
}
func (store *runnerStore) Acknowledge(context.Context, ClaimedJob) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.acked = true
	return nil
}
func (store *runnerStore) Retry(context.Context, ClaimedJob, error) (bool, error) { return false, nil }
func (store *runnerStore) Release(context.Context, ClaimedJob) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.released = true
	return nil
}
func (store *runnerStore) DeadLetters(context.Context, int64) ([]DeadJob, error) { return nil, nil }
func (store *runnerStore) Stats(context.Context) (Stats, error)                  { return Stats{}, nil }

func TestRunnerReleasesActiveJobAfterShutdownGrace(t *testing.T) {
	store := &runnerStore{job: ClaimedJob{Job: Job{Version: EnvelopeVersion, ID: "job", Kind: "blocked", MaxAttempts: 3}, Receipt: "receipt"}}
	started := make(chan struct{})
	handler := func(ctx context.Context, _ Job) error { close(started); <-ctx.Done(); return ctx.Err() }
	runner := NewRunner(store, map[string]Handler{"blocked": handler}, nil, RunnerConfig{HandleTimeout: time.Minute, ShutdownGrace: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.released {
		t.Fatal("expected active job to be safely released")
	}
}

func TestRunnerCompletesActiveJobDuringShutdownGrace(t *testing.T) {
	store := &runnerStore{job: ClaimedJob{Job: Job{Version: EnvelopeVersion, ID: "job", Kind: "finish", MaxAttempts: 3}, Receipt: "receipt"}}
	started := make(chan struct{})
	finish := make(chan struct{})
	handler := func(context.Context, Job) error { close(started); <-finish; return nil }
	runner := NewRunner(store, map[string]Handler{"finish": handler}, nil, RunnerConfig{HandleTimeout: time.Minute, ShutdownGrace: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-started
	cancel()
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.acked || store.released {
		t.Fatalf("expected graceful acknowledgement, got acked=%v released=%v", store.acked, store.released)
	}
}
