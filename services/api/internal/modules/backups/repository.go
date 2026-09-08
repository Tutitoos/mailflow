package backups

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultRunLimit = 25
	MaxRunLimit     = 100
	HistoryLimit    = 365
	RuntimeStale    = 5 * time.Minute
	MaximumRunAge   = 6 * time.Hour
)

var (
	ErrUnavailable = errors.New("backup state unavailable")
	ErrRunActive   = errors.New("a backup is already running")
)

var snapshotPattern = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

type Run struct {
	ID             string     `json:"id"`
	Trigger        string     `json:"trigger"`
	RepositoryKind string     `json:"repositoryKind"`
	State          string     `json:"state"`
	SnapshotID     string     `json:"snapshotId,omitempty"`
	ErrorCode      string     `json:"errorCode,omitempty"`
	FileCount      int64      `json:"fileCount"`
	ByteCount      int64      `json:"byteCount"`
	ScheduledFor   time.Time  `json:"scheduledFor"`
	StartedAt      time.Time  `json:"startedAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
}

type Runtime struct {
	Enabled        bool      `json:"enabled"`
	RepositoryKind string    `json:"repositoryKind"`
	Schedule       string    `json:"schedule"`
	Timezone       string    `json:"timezone"`
	NextRunAt      time.Time `json:"nextRunAt"`
	HeartbeatAt    time.Time `json:"heartbeatAt"`
}

type Status struct {
	Configured    bool       `json:"configured"`
	State         string     `json:"state"`
	Runtime       *Runtime   `json:"runtime,omitempty"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	Runs          []Run      `json:"runs"`
}

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (repository *Repository) Begin(ctx context.Context, trigger, repositoryKind string, scheduledFor time.Time) (Run, error) {
	if repository == nil || repository.pool == nil || !validTrigger(trigger) || !validRepositoryKind(repositoryKind) {
		return Run{}, ErrUnavailable
	}
	id, err := ids.New()
	if err != nil {
		return Run{}, fmt.Errorf("%w: create run ID", ErrUnavailable)
	}
	var run Run
	err = repository.pool.QueryRow(ctx, `insert into backup_runs
		(id,trigger,repository_kind,state,scheduled_for) values ($1::uuid,$2,$3,'running',$4)
		on conflict do nothing
		returning id::text,trigger,repository_kind,state,coalesce(snapshot_id,''),coalesce(error_code,''),file_count,byte_count,scheduled_for,started_at,completed_at`,
		id, trigger, repositoryKind, scheduledFor.UTC(),
	).Scan(&run.ID, &run.Trigger, &run.RepositoryKind, &run.State, &run.SnapshotID, &run.ErrorCode, &run.FileCount, &run.ByteCount, &run.ScheduledFor, &run.StartedAt, &run.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunActive
	}
	if err != nil {
		return Run{}, fmt.Errorf("%w: begin run", ErrUnavailable)
	}
	return normalizeRun(run), nil
}

func (repository *Repository) Finish(ctx context.Context, id, state, snapshotID, errorCode string, fileCount, byteCount int64, now time.Time) (Run, error) {
	validSuccess := state == "succeeded" && snapshotPattern.MatchString(snapshotID) && errorCode == ""
	validFailure := state == "failed" && snapshotID == "" && validErrorCode(errorCode)
	if repository == nil || repository.pool == nil || (!validSuccess && !validFailure) || fileCount < 0 || byteCount < 0 {
		return Run{}, ErrUnavailable
	}
	var run Run
	err := repository.pool.QueryRow(ctx, `update backup_runs
		set state=$2,snapshot_id=nullif($3,''),error_code=nullif($4,''),file_count=$5,byte_count=$6,completed_at=$7
		where id=$1::uuid and state='running'
		returning id::text,trigger,repository_kind,state,coalesce(snapshot_id,''),coalesce(error_code,''),file_count,byte_count,scheduled_for,started_at,completed_at`,
		id, state, snapshotID, errorCode, fileCount, byteCount, now.UTC(),
	).Scan(&run.ID, &run.Trigger, &run.RepositoryKind, &run.State, &run.SnapshotID, &run.ErrorCode, &run.FileCount, &run.ByteCount, &run.ScheduledFor, &run.StartedAt, &run.CompletedAt)
	if err != nil {
		return Run{}, fmt.Errorf("%w: finish run", ErrUnavailable)
	}
	return normalizeRun(run), nil
}

func (repository *Repository) MarkInterrupted(ctx context.Context, now time.Time) error {
	if repository == nil || repository.pool == nil {
		return ErrUnavailable
	}
	_, err := repository.pool.Exec(ctx, `update backup_runs set state='failed',error_code='interrupted',completed_at=$1 where state='running'`, now.UTC())
	if err != nil {
		return fmt.Errorf("%w: mark interrupted runs", ErrUnavailable)
	}
	return nil
}

func (repository *Repository) UpdateRuntime(ctx context.Context, runtime Runtime) error {
	if repository == nil || repository.pool == nil || !validRepositoryKind(runtime.RepositoryKind) || runtime.Schedule == "" || runtime.Timezone == "" || runtime.NextRunAt.IsZero() {
		return ErrUnavailable
	}
	_, err := repository.pool.Exec(ctx, `insert into backup_runtime
		(singleton,enabled,repository_kind,schedule,timezone,next_run_at,heartbeat_at,updated_at)
		values (true,$1,$2,$3,$4,$5,$6,$6)
		on conflict (singleton) do update set enabled=excluded.enabled,repository_kind=excluded.repository_kind,
		schedule=excluded.schedule,timezone=excluded.timezone,next_run_at=excluded.next_run_at,
		heartbeat_at=excluded.heartbeat_at,updated_at=excluded.updated_at`,
		runtime.Enabled, runtime.RepositoryKind, runtime.Schedule, runtime.Timezone, runtime.NextRunAt.UTC(), runtime.HeartbeatAt.UTC())
	if err != nil {
		return fmt.Errorf("%w: update runtime", ErrUnavailable)
	}
	return nil
}

func (repository *Repository) Status(ctx context.Context, now time.Time, limit int) (Status, error) {
	if repository == nil || repository.pool == nil {
		return Status{}, ErrUnavailable
	}
	if limit <= 0 {
		limit = DefaultRunLimit
	}
	if limit > MaxRunLimit {
		limit = MaxRunLimit
	}
	runs, err := repository.listRuns(ctx, limit)
	if err != nil {
		return Status{}, err
	}
	var runtime Runtime
	err = repository.pool.QueryRow(ctx, `select enabled,repository_kind,schedule,timezone,next_run_at,heartbeat_at from backup_runtime where singleton=true`).Scan(
		&runtime.Enabled, &runtime.RepositoryKind, &runtime.Schedule, &runtime.Timezone, &runtime.NextRunAt, &runtime.HeartbeatAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return summarizeStatus(Status{State: "stale", Runs: runs}, now), nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("%w: read runtime", ErrUnavailable)
	}
	result := Status{Configured: true, State: "healthy", Runtime: &runtime, Runs: runs}
	if !runtime.Enabled || now.UTC().Sub(runtime.HeartbeatAt.UTC()) > RuntimeStale || runtime.HeartbeatAt.After(now.UTC().Add(time.Minute)) {
		result.State = "stale"
	}
	return summarizeStatus(result, now), nil
}

func summarizeStatus(result Status, now time.Time) Status {
	for _, run := range result.Runs {
		if run.State == "running" && now.UTC().Sub(run.StartedAt) > MaximumRunAge {
			result.State = "blocked"
		}
		if run.State == "succeeded" && result.LastSuccessAt == nil {
			if run.CompletedAt != nil {
				success := run.CompletedAt.UTC()
				result.LastSuccessAt = &success
			}
		}
	}
	if len(result.Runs) > 0 && result.Runs[0].State == "failed" && result.State == "healthy" {
		result.State = "degraded"
	}
	return result
}

func (repository *Repository) PruneHistory(ctx context.Context) error {
	if repository == nil || repository.pool == nil {
		return ErrUnavailable
	}
	_, err := repository.pool.Exec(ctx, `delete from backup_runs where id in (
		select id from backup_runs where state <> 'running' order by started_at desc,id desc offset $1
	)`, HistoryLimit)
	if err != nil {
		return fmt.Errorf("%w: prune history", ErrUnavailable)
	}
	return nil
}

func (repository *Repository) listRuns(ctx context.Context, limit int) ([]Run, error) {
	rows, err := repository.pool.Query(ctx, `select id::text,trigger,repository_kind,state,coalesce(snapshot_id,''),coalesce(error_code,''),file_count,byte_count,scheduled_for,started_at,completed_at
		from backup_runs order by started_at desc,id desc limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: list runs", ErrUnavailable)
	}
	defer rows.Close()
	runs := make([]Run, 0, limit)
	for rows.Next() {
		var run Run
		if err := rows.Scan(&run.ID, &run.Trigger, &run.RepositoryKind, &run.State, &run.SnapshotID, &run.ErrorCode, &run.FileCount, &run.ByteCount, &run.ScheduledFor, &run.StartedAt, &run.CompletedAt); err != nil {
			return nil, fmt.Errorf("%w: scan run", ErrUnavailable)
		}
		runs = append(runs, normalizeRun(run))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate runs", ErrUnavailable)
	}
	return runs, nil
}

func normalizeRun(run Run) Run {
	run.ScheduledFor = run.ScheduledFor.UTC()
	run.StartedAt = run.StartedAt.UTC()
	if run.CompletedAt != nil {
		completed := run.CompletedAt.UTC()
		run.CompletedAt = &completed
	}
	return run
}

func validTrigger(value string) bool        { return value == "scheduled" || value == "command" }
func validRepositoryKind(value string) bool { return value == "local" || value == "s3" }

func validErrorCode(value string) bool {
	switch value {
	case "interrupted", "staging_failed", "dump_failed", "repository_failed", "snapshot_failed", "verification_failed", "retention_failed":
		return true
	default:
		return false
	}
}
