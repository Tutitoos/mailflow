package backups

import (
	"context"
	"errors"
	"time"
)

const HeartbeatInterval = 30 * time.Second

type RuntimeStore interface {
	UpdateRuntime(context.Context, Runtime) error
	MarkInterrupted(context.Context, time.Time) error
}

type Scheduler struct {
	store    RuntimeStore
	service  *Service
	schedule DailySchedule
	enabled  bool
	now      func() time.Time
	report   func(error)
}

func NewScheduler(store RuntimeStore, service *Service, schedule DailySchedule, enabled bool) (*Scheduler, error) {
	if store == nil || service == nil {
		return nil, ErrUnavailable
	}
	return &Scheduler{store: store, service: service, schedule: schedule, enabled: enabled, now: time.Now}, nil
}

func (scheduler *Scheduler) Run(ctx context.Context) error {
	now := scheduler.now().UTC()
	if err := scheduler.store.MarkInterrupted(ctx, now); err != nil {
		return err
	}
	for {
		now = scheduler.now().UTC()
		next := scheduler.schedule.Next(now)
		if err := scheduler.heartbeat(ctx, now, next); err != nil {
			return err
		}
		if !scheduler.enabled {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(HeartbeatInterval):
				continue
			}
		}
		timer := time.NewTimer(time.Until(next))
		ticker := time.NewTicker(HeartbeatInterval)
		fired := false
		for !fired {
			select {
			case <-ctx.Done():
				timer.Stop()
				ticker.Stop()
				return ctx.Err()
			case tick := <-ticker.C:
				if err := scheduler.heartbeat(ctx, tick.UTC(), next); err != nil {
					timer.Stop()
					ticker.Stop()
					return err
				}
			case <-timer.C:
				fired = true
			}
		}
		ticker.Stop()
		if _, err := scheduler.service.RunOnce(ctx, "scheduled", next); err != nil && scheduler.report != nil {
			scheduler.report(err)
		}
	}
}

func (scheduler *Scheduler) SetReporter(report func(error)) { scheduler.report = report }

func (scheduler *Scheduler) heartbeat(ctx context.Context, now, next time.Time) error {
	err := scheduler.store.UpdateRuntime(ctx, Runtime{
		Enabled: scheduler.enabled, RepositoryKind: scheduler.service.RepositoryKind(),
		Schedule: scheduler.schedule.String(), Timezone: scheduler.schedule.Timezone(),
		NextRunAt: next.UTC(), HeartbeatAt: now.UTC(),
	})
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return err
}
