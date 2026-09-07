package logs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultQueueSize     = 2000
	DefaultBatchSize     = 100
	DefaultFlushInterval = 250 * time.Millisecond
	DebugRefreshInterval = 30 * time.Second
)

type StreamFunc func(context.Context, Entry) error

type Pipeline struct {
	stdout     slog.Handler
	service    string
	module     string
	queue      chan Entry
	store      atomic.Pointer[Store]
	debugUntil atomic.Int64
	dropped    atomic.Uint64
	streamMu   sync.RWMutex
	stream     StreamFunc
}

func NewPipeline(output io.Writer, service, module string) (*Pipeline, error) {
	service = strings.ToLower(strings.TrimSpace(service))
	module = strings.ToLower(strings.TrimSpace(module))
	if output == nil || !identifierPattern.MatchString(service) || !identifierPattern.MatchString(module) {
		return nil, ErrInvalidEntry
	}
	return &Pipeline{
		stdout:  slog.NewJSONHandler(output, &slog.HandlerOptions{Level: slog.LevelDebug}),
		service: service, module: module, queue: make(chan Entry, DefaultQueueSize),
	}, nil
}

func (pipeline *Pipeline) Attach(store *Store) { pipeline.store.Store(store) }

func (pipeline *Pipeline) SetStream(stream StreamFunc) {
	pipeline.streamMu.Lock()
	pipeline.stream = stream
	pipeline.streamMu.Unlock()
}

func (pipeline *Pipeline) Dropped() uint64 { return pipeline.dropped.Load() }

func (pipeline *Pipeline) Enabled(_ context.Context, level slog.Level) bool {
	if level >= slog.LevelInfo {
		return true
	}
	return level >= slog.LevelDebug && time.Now().UnixNano() < pipeline.debugUntil.Load()
}

func (pipeline *Pipeline) Handle(ctx context.Context, record slog.Record) error {
	return pipeline.handle(ctx, record, nil, nil)
}

func (pipeline *Pipeline) WithAttrs(attributes []slog.Attr) slog.Handler {
	return &pipelineHandler{pipeline: pipeline, attributes: append([]slog.Attr(nil), attributes...)}
}

func (pipeline *Pipeline) WithGroup(name string) slog.Handler {
	return &pipelineHandler{pipeline: pipeline, groups: []string{name}}
}

type pipelineHandler struct {
	pipeline   *Pipeline
	attributes []slog.Attr
	groups     []string
}

func (handler *pipelineHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.pipeline.Enabled(ctx, level)
}
func (handler *pipelineHandler) Handle(ctx context.Context, record slog.Record) error {
	return handler.pipeline.handle(ctx, record, handler.attributes, handler.groups)
}
func (handler *pipelineHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	clone := *handler
	clone.attributes = append(append([]slog.Attr(nil), handler.attributes...), attributes...)
	return &clone
}
func (handler *pipelineHandler) WithGroup(name string) slog.Handler {
	clone := *handler
	clone.groups = append(append([]string(nil), handler.groups...), name)
	return &clone
}

func (pipeline *Pipeline) handle(ctx context.Context, record slog.Record, preset []slog.Attr, groups []string) error {
	fields := make(map[string]any, record.NumAttrs()+len(preset)+1)
	prefix := strings.Join(groups, ".")
	for _, attribute := range preset {
		collectAttribute(fields, prefix, attribute)
	}
	record.Attrs(func(attribute slog.Attr) bool { collectAttribute(fields, prefix, attribute); return true })
	requestID, _ := fields["requestId"].(string)
	if requestID == "" {
		requestID, _ = fields["request_id"].(string)
	}
	if !requestIDPattern.MatchString(requestID) {
		requestID = ""
	}
	clean := Redact(fields)
	event, _ := clean["event"].(string)
	if !eventPattern.MatchString(event) {
		event = pipeline.module + ".log"
	}
	module := pipeline.module
	if candidate := strings.SplitN(event, ".", 2)[0]; identifierPattern.MatchString(candidate) {
		module = candidate
	}
	delete(clean, "event")
	delete(clean, "requestId")
	delete(clean, "request_id")
	clean["message"] = redactString(record.Message)

	stdoutRecord := slog.NewRecord(record.Time, record.Level, redactString(record.Message), record.PC)
	stdoutRecord.AddAttrs(slog.String("service", pipeline.service), slog.String("module", module), slog.String("event", event))
	if requestID != "" {
		stdoutRecord.AddAttrs(slog.String("requestId", requestID))
	}
	for key, value := range clean {
		stdoutRecord.AddAttrs(slog.Any(key, value))
	}
	if err := pipeline.stdout.Handle(ctx, stdoutRecord); err != nil {
		return err
	}

	if pipeline.store.Load() == nil {
		return nil
	}
	entry := Entry{OccurredAt: record.Time.UTC(), Service: pipeline.service, Module: module, Level: levelName(record.Level), Event: event, RequestID: requestID, Attributes: clean}
	select {
	case pipeline.queue <- entry:
	default:
		pipeline.dropped.Add(1)
	}
	return nil
}

func collectAttribute(destination map[string]any, prefix string, attribute slog.Attr) {
	attribute.Value = attribute.Value.Resolve()
	key := attribute.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	if attribute.Value.Kind() == slog.KindGroup {
		for _, child := range attribute.Value.Group() {
			collectAttribute(destination, key, child)
		}
		return
	}
	destination[key] = attribute.Value.Any()
}

func levelName(level slog.Level) string {
	if level < slog.LevelInfo {
		return "debug"
	}
	if level < slog.LevelWarn {
		return "info"
	}
	if level < slog.LevelError {
		return "warning"
	}
	return "error"
}

func (pipeline *Pipeline) Run(ctx context.Context) error {
	ticker := time.NewTicker(DefaultFlushInterval)
	debugTicker := time.NewTicker(DebugRefreshInterval)
	retentionTicker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	defer debugTicker.Stop()
	defer retentionTicker.Stop()
	pipeline.refreshDebug(ctx)
	_, _ = pipeline.Cleanup(ctx, time.Now().UTC())
	pending := make([]Entry, 0, DefaultBatchSize)
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = pipeline.flush(shutdown, pending)
			return ctx.Err()
		case entry := <-pipeline.queue:
			shouldFlush := len(pending) == DefaultBatchSize-1
			if len(pending) < DefaultBatchSize {
				pending = append(pending, entry)
			} else {
				pipeline.dropped.Add(1)
			}
			if shouldFlush {
				if err := pipeline.flush(ctx, pending); err == nil {
					pending = pending[:0]
				}
			}
		case <-ticker.C:
			if len(pending) > 0 {
				if err := pipeline.flush(ctx, pending); err == nil {
					pending = pending[:0]
				}
			}
		case <-debugTicker.C:
			pipeline.refreshDebug(ctx)
		case now := <-retentionTicker.C:
			_, _ = pipeline.Cleanup(ctx, now)
		}
	}
}

func (pipeline *Pipeline) flush(ctx context.Context, entries []Entry) error {
	store := pipeline.store.Load()
	if store == nil || len(entries) == 0 {
		return nil
	}
	stored, err := store.WriteBatch(ctx, entries)
	if err != nil {
		return err
	}
	pipeline.streamMu.RLock()
	stream := pipeline.stream
	pipeline.streamMu.RUnlock()
	if stream == nil {
		return nil
	}
	for _, entry := range stored {
		streamContext, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := stream(streamContext, entry)
		cancel()
		if err != nil {
			continue
		}
	}
	return nil
}

func (pipeline *Pipeline) SetDebug(ctx context.Context, now time.Time, duration time.Duration) (*time.Time, error) {
	store := pipeline.store.Load()
	if store == nil {
		return nil, errors.New("log store unavailable")
	}
	until, err := store.SetDebug(ctx, now, duration)
	if err != nil {
		return nil, err
	}
	pipeline.applyDebug(until, now)
	return until, nil
}

func (pipeline *Pipeline) DebugUntil(ctx context.Context, now time.Time) (*time.Time, error) {
	store := pipeline.store.Load()
	if store == nil {
		return nil, errors.New("log store unavailable")
	}
	until, err := store.DebugUntil(ctx)
	if err != nil {
		return nil, err
	}
	pipeline.applyDebug(until, now)
	if until != nil && !until.After(now) {
		return nil, nil
	}
	return until, nil
}

func (pipeline *Pipeline) Query(ctx context.Context, query Query) ([]Entry, error) {
	store := pipeline.store.Load()
	if store == nil {
		return nil, errors.New("log store unavailable")
	}
	return store.Query(ctx, query)
}

func (pipeline *Pipeline) Cleanup(ctx context.Context, now time.Time) (int64, error) {
	store := pipeline.store.Load()
	if store == nil {
		return 0, errors.New("log store unavailable")
	}
	return store.Cleanup(ctx, now)
}

func (pipeline *Pipeline) refreshDebug(ctx context.Context) {
	store := pipeline.store.Load()
	if store == nil {
		return
	}
	until, err := store.DebugUntil(ctx)
	if err == nil {
		pipeline.applyDebug(until, time.Now().UTC())
	}
}

func (pipeline *Pipeline) applyDebug(until *time.Time, now time.Time) {
	if until == nil || !until.After(now) {
		pipeline.debugUntil.Store(0)
		return
	}
	pipeline.debugUntil.Store(until.UnixNano())
}

func StreamPayload(entry Entry) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{"id": entry.ID, "occurredAt": entry.OccurredAt, "service": entry.Service, "module": entry.Module, "level": entry.Level, "event": entry.Event, "requestId": entry.RequestID})
	return payload
}
