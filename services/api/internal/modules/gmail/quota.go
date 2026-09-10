package gmail

import (
	"context"
	"sync"
	"time"
)

const defaultQuotaUnitsPerMinute = 5400

// QuotaLimiter spaces requests according to their documented Gmail quota cost.
type QuotaLimiter interface {
	Wait(context.Context, int) error
}

type quotaClock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

type systemQuotaClock struct{}

func (systemQuotaClock) Now() time.Time { return time.Now() }

func (systemQuotaClock) Wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type quotaLimiter struct {
	mu           sync.Mutex
	next         time.Time
	unitInterval time.Duration
	clock        quotaClock
}

// NewQuotaLimiter leaves ten percent of the documented per-user budget unused
// so normal timing variance cannot push an account over Gmail's minute limit.
func NewQuotaLimiter() QuotaLimiter {
	return newQuotaLimiter(defaultQuotaUnitsPerMinute, systemQuotaClock{})
}

func newQuotaLimiter(unitsPerMinute int, clock quotaClock) *quotaLimiter {
	return &quotaLimiter{
		unitInterval: time.Minute / time.Duration(unitsPerMinute),
		clock:        clock,
	}
}

func (limiter *quotaLimiter) Wait(ctx context.Context, units int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if units < 1 {
		units = 1
	}

	limiter.mu.Lock()
	now := limiter.clock.Now()
	ready := limiter.next
	if ready.Before(now) {
		ready = now
	}
	ready = ready.Add(time.Duration(units) * limiter.unitInterval)
	limiter.next = ready
	limiter.mu.Unlock()

	return limiter.clock.Wait(ctx, ready.Sub(now))
}

func quotaCost(method, path string) int {
	switch {
	case method == "GET" && path == "/profile":
		return 1
	case method == "GET" && path == "/labels":
		return 1
	case method == "GET" && path == "/history":
		return 2
	case method == "GET" && path == "/messages":
		return 5
	case method == "GET" && hasPathSegment(path, "/attachments/"):
		return 20
	case method == "GET" && hasPathPrefix(path, "/messages/"):
		return 20
	case method == "POST" && path == "/messages/send":
		return 100
	case method == "POST" && path == "/drafts/send":
		return 100
	case method == "POST" && path == "/drafts":
		return 10
	case method == "PUT" && hasPathPrefix(path, "/drafts/"):
		return 15
	case method == "DELETE" && hasPathPrefix(path, "/drafts/"):
		return 10
	case method == "POST" && hasPathPrefix(path, "/messages/") && hasPathSuffix(path, "/modify"):
		return 5
	case method == "POST" && hasPathPrefix(path, "/messages/") && hasPathSuffix(path, "/trash"):
		return 20
	case method == "POST" && hasPathPrefix(path, "/messages/") && hasPathSuffix(path, "/untrash"):
		return 5
	case method == "POST" && hasPathPrefix(path, "/threads/") && hasPathSuffix(path, "/modify"):
		return 10
	case method == "POST" && hasPathPrefix(path, "/threads/") && hasPathSuffix(path, "/trash"):
		return 20
	case method == "POST" && hasPathPrefix(path, "/threads/") && hasPathSuffix(path, "/untrash"):
		return 10
	default:
		// Fail closed when a new Gmail method is added without an explicit cost.
		return 100
	}
}

func hasPathPrefix(path, prefix string) bool {
	return len(path) > len(prefix) && path[:len(prefix)] == prefix
}
func hasPathSuffix(path, suffix string) bool {
	return len(path) > len(suffix) && path[len(path)-len(suffix):] == suffix
}
func hasPathSegment(path, segment string) bool {
	for index := 0; index+len(segment) <= len(path); index++ {
		if path[index:index+len(segment)] == segment {
			return true
		}
	}
	return false
}
