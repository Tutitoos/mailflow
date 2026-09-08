package admin

import (
	"context"
	"errors"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

const (
	HeartbeatInterval = 10 * time.Second
	HeartbeatTTL      = 2 * time.Minute
)

var ErrHeartbeatMissing = errors.New("component heartbeat is missing")

type Heartbeats struct {
	client redis.UniversalClient
	prefix string
}

func NewHeartbeats(client redis.UniversalClient, prefix string) (*Heartbeats, error) {
	prefix = strings.TrimSpace(prefix)
	if client == nil || prefix == "" {
		return nil, errors.New("admin heartbeat requires Redis and a key prefix")
	}
	return &Heartbeats{client: client, prefix: prefix}, nil
}

func (heartbeats *Heartbeats) Touch(ctx context.Context, component string, now time.Time) error {
	if !validComponent(component) {
		return errors.New("invalid heartbeat component")
	}
	return heartbeats.client.Set(ctx, heartbeats.key(component), now.UTC().Format(time.RFC3339Nano), HeartbeatTTL).Err()
}

func (heartbeats *Heartbeats) LastSeen(ctx context.Context, component string) (time.Time, error) {
	if !validComponent(component) {
		return time.Time{}, errors.New("invalid heartbeat component")
	}
	value, err := heartbeats.client.Get(ctx, heartbeats.key(component)).Result()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, ErrHeartbeatMissing
	}
	if err != nil {
		return time.Time{}, err
	}
	observed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, ErrHeartbeatMissing
	}
	return observed.UTC(), nil
}

func (heartbeats *Heartbeats) Run(ctx context.Context, component string) error {
	if err := heartbeats.Touch(ctx, component, time.Now().UTC()); err != nil {
		return err
	}
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if err := heartbeats.Touch(ctx, component, now); err != nil {
				return err
			}
		}
	}
}

func (heartbeats *Heartbeats) key(component string) string {
	return heartbeats.prefix + ":admin:heartbeat:" + component
}

func validComponent(component string) bool {
	if component == "" || len(component) > 32 {
		return false
	}
	for _, character := range component {
		if (character < 'a' || character > 'z') && character != '-' {
			return false
		}
	}
	return true
}
