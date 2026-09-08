package imap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WatchReason string

const (
	WatchChanged   WatchReason = "changed"
	WatchHeartbeat WatchReason = "heartbeat"
	WatchPoll      WatchReason = "poll"
)

var (
	ErrWatchConfiguration = errors.New("IMAP watch configuration is invalid")
	ErrWatchDisconnected  = errors.New("IMAP watch connection was dropped")
	ErrWatchLeaseHeld     = errors.New("IMAP watch lease is already held")
	ErrWatchLeaseLost     = errors.New("IMAP watch lease ownership was lost")
)

type WatchConfig struct {
	AccountRefresh time.Duration
	Heartbeat      time.Duration
	PollInterval   time.Duration
	ReconnectMin   time.Duration
	ReconnectMax   time.Duration
	LeaseRenew     time.Duration
	BurstWindow    time.Duration
	MaxConnections int
}

func DefaultWatchConfig() WatchConfig {
	return WatchConfig{
		AccountRefresh: 30 * time.Second,
		Heartbeat:      25 * time.Minute,
		PollInterval:   5 * time.Minute,
		ReconnectMin:   time.Second,
		ReconnectMax:   time.Minute,
		LeaseRenew:     30 * time.Second,
		BurstWindow:    time.Second,
		MaxConnections: 4,
	}
}

func (config WatchConfig) valid() bool {
	return config.AccountRefresh > 0 && config.Heartbeat > 0 && config.PollInterval > 0 &&
		config.ReconnectMin > 0 && config.ReconnectMax >= config.ReconnectMin && config.LeaseRenew > 0 && config.BurstWindow >= 0 &&
		config.MaxConnections > 0 && config.MaxConnections <= 64
}

type WatchAccount struct {
	UserID      string
	AccountID   string
	Credentials storedCredentials
}

type WatchAccountSource interface {
	Active(context.Context) ([]WatchAccount, error)
}

type WatchSession interface {
	IdleSupported() bool
	Wait(context.Context, time.Duration) (WatchReason, error)
	Close() error
}

type WatchSessionFactory interface {
	Open(context.Context, storedCredentials) (WatchSession, error)
}

type WatchNotifier interface {
	Notify(context.Context, WatchAccount, WatchReason) error
}

type WatchEvent struct {
	Operation string
	Result    string
	Reason    WatchReason
}

type WatchObserver interface{ Observe(WatchEvent) }

type WatchObserverFunc func(WatchEvent)

func (observe WatchObserverFunc) Observe(event WatchEvent) {
	if observe != nil {
		observe(event)
	}
}

type WatchLease struct {
	AccountID string
	token     string
}

type WatchLeases interface {
	Acquire(context.Context, string) (WatchLease, error)
	Renew(context.Context, WatchLease) error
	Release(context.Context, WatchLease) error
}

type WatchSupervisor struct {
	source   WatchAccountSource
	factory  WatchSessionFactory
	notifier WatchNotifier
	leases   WatchLeases
	config   WatchConfig
	now      func() time.Time
	observer WatchObserver
}

func (supervisor *WatchSupervisor) SetObserver(observer WatchObserver) {
	supervisor.observer = observer
}

func (supervisor *WatchSupervisor) observe(operation, result string, reason WatchReason) {
	if supervisor.observer != nil {
		supervisor.observer.Observe(WatchEvent{Operation: operation, Result: result, Reason: reason})
	}
}

func NewWatchSupervisor(source WatchAccountSource, factory WatchSessionFactory, notifier WatchNotifier, leases WatchLeases, config WatchConfig) (*WatchSupervisor, error) {
	if source == nil || factory == nil || notifier == nil || leases == nil || !config.valid() {
		return nil, ErrWatchConfiguration
	}
	return &WatchSupervisor{source: source, factory: factory, notifier: notifier, leases: leases, config: config, now: time.Now}, nil
}

func (supervisor *WatchSupervisor) Run(ctx context.Context) error {
	ticker := time.NewTicker(supervisor.config.AccountRefresh)
	defer ticker.Stop()
	type runningWatch struct{ cancel context.CancelFunc }
	running := map[string]runningWatch{}
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, supervisor.config.MaxConnections)
	reconcile := func() {
		accounts, err := supervisor.source.Active(ctx)
		if err != nil {
			supervisor.observe("accounts", "failure", "")
			if len(accounts) == 0 {
				return
			}
		}
		active := make(map[string]bool, len(accounts))
		for _, account := range accounts {
			if !validWatchAccount(account) {
				continue
			}
			active[account.AccountID] = true
			if _, exists := running[account.AccountID]; exists {
				continue
			}
			watchContext, cancel := context.WithCancel(ctx)
			running[account.AccountID] = runningWatch{cancel: cancel}
			wait.Add(1)
			go func() {
				defer wait.Done()
				supervisor.watch(watchContext, account, semaphore)
			}()
		}
		for accountID, watch := range running {
			if !active[accountID] {
				watch.cancel()
				delete(running, accountID)
			}
		}
	}
	reconcile()
	for {
		select {
		case <-ctx.Done():
			for _, watch := range running {
				watch.cancel()
			}
			wait.Wait()
			return ctx.Err()
		case <-ticker.C:
			reconcile()
		}
	}
}

func (supervisor *WatchSupervisor) watch(ctx context.Context, account WatchAccount, semaphore chan struct{}) {
	backoff := supervisor.config.ReconnectMin
	for ctx.Err() == nil {
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			return
		}
		lease, err := supervisor.leases.Acquire(ctx, account.AccountID)
		if err != nil {
			<-semaphore
			result := "failure"
			if errors.Is(err, ErrWatchLeaseHeld) {
				result = "contended"
			}
			supervisor.observe("lease", result, "")
			if !waitContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, supervisor.config.ReconnectMax)
			continue
		}
		connected, err := supervisor.watchLease(ctx, account, lease)
		releaseContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = supervisor.leases.Release(releaseContext, lease)
		cancel()
		<-semaphore
		if ctx.Err() != nil {
			return
		}
		delay := backoff
		if connected {
			backoff = supervisor.config.ReconnectMin
			delay = supervisor.config.ReconnectMin
		} else {
			backoff = nextBackoff(backoff, supervisor.config.ReconnectMax)
		}
		if !waitContext(ctx, delay) {
			return
		}
	}
}

func (supervisor *WatchSupervisor) watchLease(ctx context.Context, account WatchAccount, lease WatchLease) (bool, error) {
	session, err := supervisor.factory.Open(ctx, account.Credentials)
	if err != nil {
		supervisor.observe("connect", "failure", "")
		return false, err
	}
	supervisor.observe("connect", "success", "")
	defer func() {
		_ = session.Close()
	}()
	watchContext, cancel := context.WithCancel(ctx)
	renewal := make(chan error, 1)
	go supervisor.renewLease(watchContext, cancel, lease, renewal)
	renewalReceived := false
	defer func() {
		cancel()
		if !renewalReceived {
			<-renewal
		}
	}()
	waitDuration := supervisor.config.PollInterval
	nextPoll := supervisor.now().Add(supervisor.config.PollInterval)
	if session.IdleSupported() {
		waitDuration = supervisor.config.Heartbeat
	}
	var lastNotification time.Time
	for ctx.Err() == nil {
		reason, err := session.Wait(watchContext, waitDuration)
		if err != nil {
			supervisor.observe("wait", "failure", reason)
			cancel()
			renewalErr := <-renewal
			renewalReceived = true
			if renewalErr != nil && !errors.Is(renewalErr, context.Canceled) {
				return true, renewalErr
			}
			return true, err
		}
		if session.IdleSupported() {
			if reason == WatchChanged {
				if !lastNotification.IsZero() && supervisor.now().Sub(lastNotification) < supervisor.config.BurstWindow {
					continue
				}
				if err := supervisor.notifier.Notify(ctx, account, reason); err != nil {
					supervisor.observe("notify", "failure", reason)
					return true, err
				}
				supervisor.observe("notify", "success", reason)
				lastNotification = supervisor.now()
			}
			continue
		}
		if !supervisor.now().Before(nextPoll) {
			if err := supervisor.notifier.Notify(ctx, account, WatchPoll); err != nil {
				supervisor.observe("notify", "failure", WatchPoll)
				return true, err
			}
			supervisor.observe("notify", "success", WatchPoll)
			nextPoll = supervisor.now().Add(supervisor.config.PollInterval)
		}
	}
	return true, ctx.Err()
}

func (supervisor *WatchSupervisor) renewLease(ctx context.Context, cancel context.CancelFunc, lease WatchLease, result chan<- error) {
	ticker := time.NewTicker(supervisor.config.LeaseRenew)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result <- ctx.Err()
			return
		case <-ticker.C:
			if err := supervisor.leases.Renew(ctx, lease); err != nil {
				result <- err
				cancel()
				return
			}
		}
	}
}

func validWatchAccount(account WatchAccount) bool {
	_, userErr := uuid.Parse(account.UserID)
	_, accountErr := uuid.Parse(account.AccountID)
	return userErr == nil && accountErr == nil && account.Credentials.Username != "" && account.Credentials.Password != ""
}

func nextBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type watchCredentialAccounts interface {
	Credentials(context.Context, string, string) (json.RawMessage, error)
}

type DatabaseWatchAccounts struct {
	pool     *pgxpool.Pool
	accounts watchCredentialAccounts
}

func NewDatabaseWatchAccounts(pool *pgxpool.Pool, accountStore watchCredentialAccounts) (*DatabaseWatchAccounts, error) {
	if pool == nil || accountStore == nil {
		return nil, ErrWatchConfiguration
	}
	return &DatabaseWatchAccounts{pool: pool, accounts: accountStore}, nil
}

func (source *DatabaseWatchAccounts) Active(ctx context.Context) ([]WatchAccount, error) {
	rows, err := source.pool.Query(ctx, `select user_id::text, id::text from accounts where provider = 'imap' and disabled_at is null order by id limit 64`)
	if err != nil {
		return nil, fmt.Errorf("list active IMAP accounts: %w", err)
	}
	defer rows.Close()
	result := make([]WatchAccount, 0)
	var loadErrors []error
	for rows.Next() {
		var account WatchAccount
		if err := rows.Scan(&account.UserID, &account.AccountID); err != nil {
			return nil, fmt.Errorf("scan active IMAP account: %w", err)
		}
		raw, err := source.accounts.Credentials(ctx, account.UserID, account.AccountID)
		if err != nil {
			loadErrors = append(loadErrors, fmt.Errorf("load active IMAP credentials: %w", err))
			continue
		}
		account.Credentials, err = decodeStoredCredentials(raw)
		if err != nil {
			loadErrors = append(loadErrors, err)
			continue
		}
		result = append(result, account)
	}
	if err := rows.Err(); err != nil {
		loadErrors = append(loadErrors, fmt.Errorf("iterate active IMAP accounts: %w", err))
	}
	return result, errors.Join(loadErrors...)
}

type folderDiscovery interface {
	DiscoverFolders(context.Context, string, string) (FolderDiscoveryResult, error)
}

type watchEventPublisher interface {
	Publish(context.Context, string, string, json.RawMessage) (events.Envelope, error)
}

type FolderWatchNotifier struct {
	folders folderDiscovery
	events  watchEventPublisher
}

func NewFolderWatchNotifier(folders folderDiscovery, publisher watchEventPublisher) (*FolderWatchNotifier, error) {
	if folders == nil || publisher == nil {
		return nil, ErrWatchConfiguration
	}
	return &FolderWatchNotifier{folders: folders, events: publisher}, nil
}

func (notifier *FolderWatchNotifier) Notify(ctx context.Context, account WatchAccount, reason WatchReason) error {
	result, err := notifier.folders.DiscoverFolders(ctx, account.UserID, account.AccountID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"accountId": account.AccountID, "reason": reason, "folderCount": len(result.Folders),
		"reconciliationRequired": result.ReconciliationRequired,
	})
	_, err = notifier.events.Publish(ctx, account.UserID, "mail.changed", payload)
	return err
}

var _ WatchAccountSource = (*DatabaseWatchAccounts)(nil)
var _ WatchNotifier = (*FolderWatchNotifier)(nil)
