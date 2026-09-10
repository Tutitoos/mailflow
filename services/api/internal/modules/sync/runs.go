package sync

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type RunPhase string
type RunState string

const (
	PhaseRecent      RunPhase = "recent"
	PhaseHistorical  RunPhase = "historical"
	PhaseIncremental RunPhase = "incremental"
	PhaseReconcile   RunPhase = "reconcile"

	RunQueued    RunState = "queued"
	RunRunning   RunState = "running"
	RunCompleted RunState = "completed"
	RunCancelled RunState = "cancelled"
	RunFailed    RunState = "failed"

	RecentWindow         = 90 * 24 * time.Hour
	ReconciliationPeriod = 24 * time.Hour
	ActivePollInterval   = 2 * time.Minute
	IdlePollInterval     = 10 * time.Minute
	SyncExecuteJobKind   = "sync.execute"
	DefaultSyncRunBatch  = 100
)

var (
	ErrInvalidRun          = errors.New("invalid sync run")
	ErrRunNotFound         = errors.New("sync run not found")
	ErrRunExists           = errors.New("active sync run already exists")
	ErrRunStale            = errors.New("sync run delivery is stale")
	ErrRemoteCursorInvalid = errors.New("remote sync cursor is invalid")
)

type Run struct {
	ID              string          `json:"id"`
	AccountID       string          `json:"accountId"`
	Phase           RunPhase        `json:"phase"`
	State           RunState        `json:"state"`
	Checkpoint      json.RawMessage `json:"checkpoint"`
	Version         int64           `json:"version"`
	WindowStart     *time.Time      `json:"windowStart,omitempty"`
	AppliedCount    int64           `json:"appliedCount"`
	CancelRequested bool            `json:"cancelRequested"`
	ScheduledFor    time.Time       `json:"scheduledFor"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	LastSuccessAt   *time.Time      `json:"lastSuccessAt,omitempty"`
	CompletedAt     *time.Time      `json:"completedAt,omitempty"`
	FailureCode     string          `json:"failureCode,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

type DueRun struct {
	UserID string
	Run    Run
}

type CreateRunInput struct {
	UserID       string
	AccountID    string
	Phase        RunPhase
	Checkpoint   json.RawMessage
	WindowStart  *time.Time
	ScheduledFor time.Time
}

type CommitPageInput struct {
	UserID          string
	AccountID       string
	RunID           string
	ExpectedVersion int64
	Checkpoint      json.RawMessage
	AppliedCount    int64
	HasMore         bool
	CompletedAt     time.Time
	Apply           func(context.Context, pgx.Tx) error
}

type RunStore interface {
	CreateRun(context.Context, CreateRunInput) (Run, error)
	GetRun(context.Context, string, string, string) (Run, error)
	StartRun(context.Context, string, string, string, int64, time.Time) (Run, error)
	CommitPage(context.Context, CommitPageInput) (Run, error)
	RequeueRun(context.Context, string, string, string, int64, time.Time) error
	FailRun(context.Context, string, string, string, int64, string, time.Time) (Run, error)
	RecoverFailedRun(context.Context, string, string, time.Time) (Run, error)
	CancelRun(context.Context, string, string, string, time.Time) (Run, error)
	DueRuns(context.Context, time.Time, int) ([]DueRun, error)
	ExpediteReconciliation(context.Context, string, string, time.Time) (Run, error)
	ExpediteIncremental(context.Context, string, string, time.Time) (Run, error)
}
