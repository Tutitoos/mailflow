package admin

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HealthState string

const (
	Healthy  HealthState = "healthy"
	Degraded HealthState = "degraded"
	Blocked  HealthState = "blocked"
	Stale    HealthState = "stale"

	WorkerStaleAfter = 45 * time.Second
)

type Component struct {
	Name           string      `json:"name"`
	Status         HealthState `json:"status"`
	Detail         string      `json:"detail"`
	CheckedAt      time.Time   `json:"checkedAt"`
	LastObservedAt *time.Time  `json:"lastObservedAt,omitempty"`
}

type Status struct {
	State      HealthState `json:"state"`
	Version    string      `json:"version"`
	GoVersion  string      `json:"goVersion"`
	CheckedAt  time.Time   `json:"checkedAt"`
	Components []Component `json:"components"`
	Queue      queue.Stats `json:"queue"`
}

type QueueReader interface {
	Ping(context.Context) error
	Stats(context.Context) (queue.Stats, error)
}

type QueueOperator interface {
	DeadLetters(context.Context, int64) ([]queue.DeadJob, error)
	ResolveDeadLetter(context.Context, string) (bool, error)
}

type HeartbeatReader interface {
	LastSeen(context.Context, string) (time.Time, error)
}

type Options struct {
	DatabaseProbe func(context.Context) error
	Queue         QueueReader
	Heartbeats    HeartbeatReader
	Pool          *pgxpool.Pool
}

type Service struct {
	version       string
	metrics       *metrics.Service
	databaseProbe func(context.Context) error
	queue         QueueReader
	queueOperator QueueOperator
	heartbeats    HeartbeatReader
	pool          *pgxpool.Pool
	now           func() time.Time
}

func NewService(version string, registry *metrics.Registry) *Service {
	return NewServiceWithMetrics(version, metrics.NewService(registry, nil))
}

func NewServiceWithMetrics(version string, service *metrics.Service, options ...Options) *Service {
	configured := Options{}
	if len(options) > 0 {
		configured = options[0]
	}
	result := &Service{
		version: version, metrics: service, databaseProbe: configured.DatabaseProbe,
		queue: configured.Queue, heartbeats: configured.Heartbeats, pool: configured.Pool,
		now: func() time.Time { return time.Now().UTC() },
	}
	result.queueOperator, _ = configured.Queue.(QueueOperator)
	return result
}

func (service *Service) Status(ctx context.Context) Status {
	now := service.now().UTC()
	components := []Component{{Name: "api", Status: Healthy, Detail: "available", CheckedAt: now, LastObservedAt: timePointer(now)}}
	components = append(components, service.probeDatabase(ctx, now))
	queueStats, redisComponent, queueComponent := service.probeQueue(ctx, now)
	components = append(components, redisComponent, service.probeWorker(ctx, now), queueComponent)
	return Status{
		State: aggregateState(components), Version: service.version, GoVersion: runtime.Version(),
		CheckedAt: now, Components: components, Queue: queueStats,
	}
}

func (service *Service) Metrics(ctx context.Context, query metrics.Query) ([]metrics.SeriesPoint, error) {
	return service.metrics.Query(ctx, query)
}

func (service *Service) probeDatabase(ctx context.Context, now time.Time) Component {
	component := Component{Name: "postgres", Status: Stale, Detail: "not_configured", CheckedAt: now}
	if service.databaseProbe == nil {
		return component
	}
	if err := service.databaseProbe(ctx); err != nil {
		component.Status, component.Detail = Blocked, "unavailable"
		return component
	}
	component.Status, component.Detail, component.LastObservedAt = Healthy, "available", timePointer(now)
	return component
}

func (service *Service) probeQueue(ctx context.Context, now time.Time) (queue.Stats, Component, Component) {
	redisComponent := Component{Name: "redis", Status: Stale, Detail: "not_configured", CheckedAt: now}
	queueComponent := Component{Name: "queue", Status: Stale, Detail: "not_configured", CheckedAt: now}
	if service.queue == nil {
		return queue.Stats{}, redisComponent, queueComponent
	}
	if err := service.queue.Ping(ctx); err != nil {
		redisComponent.Status, redisComponent.Detail = Blocked, "unavailable"
		queueComponent.Status, queueComponent.Detail = Blocked, "unavailable"
		return queue.Stats{}, redisComponent, queueComponent
	}
	redisComponent.Status, redisComponent.Detail, redisComponent.LastObservedAt = Healthy, "available", timePointer(now)
	stats, err := service.queue.Stats(ctx)
	if err != nil {
		queueComponent.Status, queueComponent.Detail = Blocked, "unavailable"
		return queue.Stats{}, redisComponent, queueComponent
	}
	queueComponent.Status, queueComponent.Detail, queueComponent.LastObservedAt = Healthy, "empty", timePointer(now)
	if stats.Dead > 0 || stats.Retry > 0 {
		queueComponent.Status, queueComponent.Detail = Degraded, "requires_attention"
	} else if stats.Ready > 0 || stats.Pending > 0 {
		queueComponent.Detail = "active"
	}
	return stats, redisComponent, queueComponent
}

func (service *Service) probeWorker(ctx context.Context, now time.Time) Component {
	component := Component{Name: "worker", Status: Stale, Detail: "heartbeat_missing", CheckedAt: now}
	if service.heartbeats == nil {
		component.Detail = "not_configured"
		return component
	}
	lastSeen, err := service.heartbeats.LastSeen(ctx, "worker")
	if errors.Is(err, ErrHeartbeatMissing) {
		return component
	}
	if err != nil {
		component.Status, component.Detail = Blocked, "heartbeat_unavailable"
		return component
	}
	lastSeen = lastSeen.UTC()
	component.LastObservedAt = &lastSeen
	if now.Sub(lastSeen) > WorkerStaleAfter || lastSeen.After(now.Add(5*time.Second)) {
		component.Status, component.Detail = Stale, "heartbeat_stale"
		return component
	}
	component.Status, component.Detail = Healthy, "available"
	return component
}

func aggregateState(components []Component) HealthState {
	result := Healthy
	for _, component := range components {
		switch component.Status {
		case Blocked:
			return Blocked
		case Stale:
			result = Stale
		case Degraded:
			if result == Healthy {
				result = Degraded
			}
		}
	}
	return result
}

func timePointer(value time.Time) *time.Time { return &value }
