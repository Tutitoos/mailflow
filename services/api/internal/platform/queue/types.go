package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const EnvelopeVersion = 1

var ErrNoJob = errors.New("no job available")

type Job struct {
	Version     int             `json:"version"`
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Payload     json.RawMessage `json:"payload"`
	Attempt     int             `json:"attempt"`
	MaxAttempts int             `json:"maxAttempts"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type ClaimedJob struct {
	Job
	Receipt string
}

type DeadJob struct {
	Job
	Error    string    `json:"error"`
	FailedAt time.Time `json:"failedAt"`
}

// CodedError lets handlers persist a bounded operational failure code without
// placing message data, credentials, or provider responses in Redis.
type CodedError interface {
	error
	JobErrorCode() string
}

type EnqueueOptions struct {
	IdempotencyKey string
	MaxAttempts    int
}

type Config struct {
	Prefix             string
	Consumer           string
	ClaimTimeout       time.Duration
	ReadBlock          time.Duration
	IdempotencyTTL     time.Duration
	DefaultMaxAttempts int
	BaseBackoff        time.Duration
	MaxBackoff         time.Duration
	MaxPayloadBytes    int
}

func DefaultConfig() Config {
	return Config{
		Prefix: "mailflow", ClaimTimeout: 6 * time.Minute, ReadBlock: time.Second,
		IdempotencyTTL: 24 * time.Hour, DefaultMaxAttempts: 5,
		BaseBackoff: time.Second, MaxBackoff: 5 * time.Minute, MaxPayloadBytes: 256 << 10,
	}
}

type Store interface {
	Ping(context.Context) error
	Enqueue(context.Context, string, json.RawMessage, EnqueueOptions) (Job, bool, error)
	Claim(context.Context) (ClaimedJob, error)
	Acknowledge(context.Context, ClaimedJob) error
	Retry(context.Context, ClaimedJob, error) (bool, error)
	Release(context.Context, ClaimedJob) error
	DeadLetters(context.Context, int64) ([]DeadJob, error)
	Stats(context.Context) (Stats, error)
}

type Stats struct {
	Ready   int64 `json:"ready"`
	Pending int64 `json:"pending"`
	Retry   int64 `json:"retry"`
	Dead    int64 `json:"dead"`
}

type Event struct {
	Operation string
	Result    string
}

type Observer interface{ Observe(Event) }
type ObserverFunc func(Event)

func (observer ObserverFunc) Observe(event Event) { observer(event) }

type Handler func(context.Context, Job) error
