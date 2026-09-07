package queue

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type RunnerConfig struct {
	HandleTimeout time.Duration
	ShutdownGrace time.Duration
}

type Runner struct {
	store    Store
	handlers map[string]Handler
	observer Observer
	config   RunnerConfig
}

func NewRunner(store Store, handlers map[string]Handler, observer Observer, config RunnerConfig) *Runner {
	if observer == nil {
		observer = ObserverFunc(func(Event) {})
	}
	if config.HandleTimeout <= 0 {
		config.HandleTimeout = 5 * time.Minute
	}
	if config.ShutdownGrace <= 0 {
		config.ShutdownGrace = 15 * time.Second
	}
	return &Runner{store: store, handlers: handlers, observer: observer, config: config}
}

func (runner *Runner) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		job, err := runner.store.Claim(ctx)
		if errors.Is(err, ErrNoJob) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("claim queue job: %w", err)
		}
		runner.observer.Observe(Event{Operation: "claim", Result: "success"})
		if err := runner.process(ctx, job); err != nil {
			return err
		}
	}
}

func (runner *Runner) process(runContext context.Context, job ClaimedJob) error {
	handler, ok := runner.handlers[job.Kind]
	if !ok {
		handler = func(context.Context, Job) error { return fmt.Errorf("no handler registered for job kind %q", job.Kind) }
	}
	handlerContext, cancel := context.WithTimeout(context.Background(), runner.config.HandleTimeout)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- handler(handlerContext, job.Job) }()

	var handlerErr error
	select {
	case handlerErr = <-result:
	case <-handlerContext.Done():
		handlerErr = handlerContext.Err()
	case <-runContext.Done():
		timer := time.NewTimer(runner.config.ShutdownGrace)
		defer timer.Stop()
		select {
		case handlerErr = <-result:
		case <-timer.C:
			cancel()
			cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			if err := runner.store.Release(cleanupContext, job); err != nil {
				return fmt.Errorf("release active job during shutdown: %w", err)
			}
			runner.observer.Observe(Event{Operation: "release", Result: "shutdown"})
			return nil
		}
	}

	cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()
	if handlerErr == nil {
		if err := runner.store.Acknowledge(cleanupContext, job); err != nil {
			return fmt.Errorf("acknowledge queue job: %w", err)
		}
		runner.observer.Observe(Event{Operation: "handle", Result: "success"})
		return nil
	}
	dead, err := runner.store.Retry(cleanupContext, job, handlerErr)
	if err != nil {
		return fmt.Errorf("retry queue job: %w", err)
	}
	resultName := "retry"
	if dead {
		resultName = "dead"
	}
	runner.observer.Observe(Event{Operation: "handle", Result: resultName})
	return nil
}
