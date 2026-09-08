package imap

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

type staticWatchAccounts struct{ accounts []WatchAccount }

func (source staticWatchAccounts) Active(context.Context) ([]WatchAccount, error) {
	return source.accounts, nil
}

type fakeWatchFactory struct {
	mu       sync.Mutex
	sessions []*fakeWatchSession
	opens    int
}

func (factory *fakeWatchFactory) Open(context.Context, storedCredentials) (WatchSession, error) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	index := factory.opens
	factory.opens++
	if index >= len(factory.sessions) {
		index = len(factory.sessions) - 1
	}
	return factory.sessions[index], nil
}

func (factory *fakeWatchFactory) openCount() int {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return factory.opens
}

type fakeWatchResult struct {
	reason WatchReason
	err    error
}

type fakeWatchSession struct {
	idle    bool
	results chan fakeWatchResult
	closed  chan struct{}
	once    sync.Once
}

func newFakeWatchSession(idle bool, results ...fakeWatchResult) *fakeWatchSession {
	channel := make(chan fakeWatchResult, len(results))
	for _, result := range results {
		channel <- result
	}
	return &fakeWatchSession{idle: idle, results: channel, closed: make(chan struct{})}
}

func (session *fakeWatchSession) IdleSupported() bool { return session.idle }
func (session *fakeWatchSession) Wait(ctx context.Context, duration time.Duration) (WatchReason, error) {
	select {
	case result := <-session.results:
		return result.reason, result.err
	case <-time.After(duration):
		if session.idle {
			return WatchHeartbeat, nil
		}
		return WatchPoll, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (session *fakeWatchSession) Close() error {
	session.once.Do(func() { close(session.closed) })
	return nil
}

type fakeWatchNotifier struct {
	mu       sync.Mutex
	reasons  []WatchReason
	notified chan struct{}
}

func (notifier *fakeWatchNotifier) Notify(_ context.Context, _ WatchAccount, reason WatchReason) error {
	notifier.mu.Lock()
	notifier.reasons = append(notifier.reasons, reason)
	notifier.mu.Unlock()
	select {
	case notifier.notified <- struct{}{}:
	default:
	}
	return nil
}

func (notifier *fakeWatchNotifier) count() int {
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	return len(notifier.reasons)
}

type fakeWatchLeases struct {
	mu       sync.Mutex
	acquired int
	renewed  int
	released int
}

type countingWatchFactory struct {
	mu      sync.Mutex
	active  int
	maximum int
	opened  chan struct{}
}

func (factory *countingWatchFactory) Open(context.Context, storedCredentials) (WatchSession, error) {
	factory.mu.Lock()
	factory.active++
	if factory.active > factory.maximum {
		factory.maximum = factory.active
	}
	factory.mu.Unlock()
	select {
	case factory.opened <- struct{}{}:
	default:
	}
	return &countingWatchSession{factory: factory}, nil
}

type countingWatchSession struct {
	factory *countingWatchFactory
	once    sync.Once
}

func (*countingWatchSession) IdleSupported() bool { return true }
func (*countingWatchSession) Wait(ctx context.Context, _ time.Duration) (WatchReason, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (session *countingWatchSession) Close() error {
	session.once.Do(func() {
		session.factory.mu.Lock()
		session.factory.active--
		session.factory.mu.Unlock()
	})
	return nil
}

func (leases *fakeWatchLeases) Acquire(context.Context, string) (WatchLease, error) {
	leases.mu.Lock()
	defer leases.mu.Unlock()
	leases.acquired++
	return WatchLease{AccountID: "0199ed3b-c950-7000-8000-000000000160", token: "0199ed3b-c950-7000-8000-000000000260"}, nil
}
func (leases *fakeWatchLeases) Renew(context.Context, WatchLease) error {
	leases.mu.Lock()
	defer leases.mu.Unlock()
	leases.renewed++
	return nil
}
func (leases *fakeWatchLeases) Release(context.Context, WatchLease) error {
	leases.mu.Lock()
	defer leases.mu.Unlock()
	leases.released++
	return nil
}

func TestWatchSupervisorReconnectsDeduplicatesBurstsAndReleasesLease(t *testing.T) {
	account := WatchAccount{UserID: "0199ed3b-c950-7000-8000-000000000060", AccountID: "0199ed3b-c950-7000-8000-000000000160", Credentials: storedCredentials{Username: "owner@example.test", Password: "app-password"}}
	first := newFakeWatchSession(true, fakeWatchResult{err: ErrWatchDisconnected})
	second := newFakeWatchSession(true, fakeWatchResult{reason: WatchChanged}, fakeWatchResult{reason: WatchChanged})
	factory := &fakeWatchFactory{sessions: []*fakeWatchSession{first, second}}
	notifier := &fakeWatchNotifier{notified: make(chan struct{}, 4)}
	leases := &fakeWatchLeases{}
	config := testWatchConfig()
	config.BurstWindow = time.Second
	supervisor, _ := NewWatchSupervisor(staticWatchAccounts{accounts: []WatchAccount{account}}, factory, notifier, leases, config)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-notifier.notified:
	case <-time.After(time.Second):
		t.Fatal("change notification timed out")
	}
	time.Sleep(20 * time.Millisecond)
	if factory.openCount() < 2 || notifier.count() != 1 {
		t.Fatalf("opens=%d notifications=%d", factory.openCount(), notifier.count())
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor shutdown leaked a goroutine")
	}
	leases.mu.Lock()
	defer leases.mu.Unlock()
	if leases.released < 2 {
		t.Fatalf("released leases=%d", leases.released)
	}
}

func TestWatchSupervisorPollsServersWithoutIdle(t *testing.T) {
	account := WatchAccount{UserID: "0199ed3b-c950-7000-8000-000000000060", AccountID: "0199ed3b-c950-7000-8000-000000000160", Credentials: storedCredentials{Username: "owner@example.test", Password: "app-password"}}
	session := newFakeWatchSession(false)
	notifier := &fakeWatchNotifier{notified: make(chan struct{}, 1)}
	supervisor, _ := NewWatchSupervisor(staticWatchAccounts{accounts: []WatchAccount{account}}, &fakeWatchFactory{sessions: []*fakeWatchSession{session}}, notifier, &fakeWatchLeases{}, testWatchConfig())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	select {
	case <-notifier.notified:
	case <-time.After(time.Second):
		t.Fatal("fallback poll timed out")
	}
	cancel()
	<-done
	if notifier.count() != 1 {
		t.Fatalf("poll notifications=%d", notifier.count())
	}
}

func TestWatchSupervisorBoundsConnectionsAndClosesThemOnShutdown(t *testing.T) {
	accounts := make([]WatchAccount, 3)
	for index := range accounts {
		accounts[index] = WatchAccount{
			UserID: "0199ed3b-c950-7000-8000-000000000060",
			AccountID: []string{
				"0199ed3b-c950-7000-8000-000000000160",
				"0199ed3b-c950-7000-8000-000000000161",
				"0199ed3b-c950-7000-8000-000000000162",
			}[index],
			Credentials: storedCredentials{Username: "owner@example.test", Password: "app-password"},
		}
	}
	factory := &countingWatchFactory{opened: make(chan struct{}, 3)}
	config := testWatchConfig()
	config.MaxConnections = 2
	supervisor, err := NewWatchSupervisor(staticWatchAccounts{accounts: accounts}, factory, &fakeWatchNotifier{notified: make(chan struct{}, 1)}, &fakeWatchLeases{}, config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	for range 2 {
		select {
		case <-factory.opened:
		case <-time.After(time.Second):
			t.Fatal("bounded connections did not open")
		}
	}
	time.Sleep(20 * time.Millisecond)
	factory.mu.Lock()
	maximum := factory.maximum
	factory.mu.Unlock()
	if maximum != 2 {
		t.Fatalf("maximum concurrent connections=%d", maximum)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bounded watcher shutdown timed out")
	}
	factory.mu.Lock()
	active := factory.active
	factory.mu.Unlock()
	if active != 0 {
		t.Fatalf("active connections after shutdown=%d", active)
	}
}

func TestRedisWatchLeaseHasExclusiveRenewableOwnership(t *testing.T) {
	client, prefix := testkit.Redis(t)
	manager, err := NewRedisWatchLeases(client, prefix, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	accountID := "0199ed3b-c950-7000-8000-000000000160"
	lease, err := manager.Acquire(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(context.Background(), accountID); !errors.Is(err, ErrWatchLeaseHeld) {
		t.Fatalf("contending lease error=%v", err)
	}
	if err := manager.Renew(context.Background(), lease); err != nil {
		t.Fatalf("renew lease: %v", err)
	}
	if err := manager.Release(context.Background(), lease); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	if _, err := manager.Acquire(context.Background(), accountID); err != nil {
		t.Fatalf("reacquire lease: %v", err)
	}
}

func testWatchConfig() WatchConfig {
	return WatchConfig{AccountRefresh: 10 * time.Millisecond, Heartbeat: 20 * time.Millisecond, PollInterval: 20 * time.Millisecond, ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond, LeaseRenew: 5 * time.Millisecond, BurstWindow: 5 * time.Millisecond, MaxConnections: 2}
}
