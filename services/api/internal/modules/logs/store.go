package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultRetention   = 30 * 24 * time.Hour
	DefaultQueryLimit  = 500
	MaxQueryLimit      = 2000
	MaxAttributesBytes = 16 << 10
	MaxDebugDuration   = time.Hour
)

var (
	ErrInvalidEntry   = errors.New("invalid log entry")
	ErrInvalidQuery   = errors.New("invalid log query")
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	eventPattern      = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,127}$`)
	requestIDPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

type Entry struct {
	ID         int64          `json:"id,omitempty"`
	OccurredAt time.Time      `json:"occurredAt"`
	Service    string         `json:"service"`
	Module     string         `json:"module"`
	Level      string         `json:"level"`
	Event      string         `json:"event"`
	RequestID  string         `json:"requestId,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type Query struct {
	From      time.Time
	Until     time.Time
	Service   string
	Module    string
	Level     string
	Event     string
	RequestID string
	Limit     int
}

type Store struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("log store requires PostgreSQL")
	}
	return &Store{pool: pool, queries: dbgen.New(pool)}, nil
}

func (store *Store) WriteBatch(ctx context.Context, entries []Entry) ([]Entry, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	if len(entries) > 256 {
		return nil, ErrInvalidEntry
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin log batch: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := store.queries.WithTx(tx)
	stored := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		entry, attributes, validateErr := prepareEntry(entry)
		if validateErr != nil {
			return nil, validateErr
		}
		row, insertErr := queries.InsertLogEntry(ctx, dbgen.InsertLogEntryParams{
			OccurredAt: timestamp(entry.OccurredAt), Service: entry.Service, Module: entry.Module,
			Level: entry.Level, Event: entry.Event, RequestID: optionalText(entry.RequestID), Attributes: attributes,
		})
		if insertErr != nil {
			return nil, fmt.Errorf("persist log entry: %w", insertErr)
		}
		entry.ID = row.ID
		stored = append(stored, entry)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit log batch: %w", err)
	}
	return stored, nil
}

func (store *Store) Query(ctx context.Context, query Query) ([]Entry, error) {
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	if query.Limit == 0 {
		query.Limit = DefaultQueryLimit
	}
	rows, err := store.queries.ListLogEntries(ctx, dbgen.ListLogEntriesParams{
		OccurredAt: timestamp(query.From.UTC()), OccurredAt_2: timestamp(query.Until.UTC()),
		Column3: query.Service, Column4: query.Module, Column5: query.Level,
		Column6: query.Event, Column7: query.RequestID, Limit: int32(query.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("query log entries: %w", err)
	}
	result := make([]Entry, 0, len(rows))
	for _, row := range rows {
		attributes := make(map[string]any)
		if err := json.Unmarshal(row.Attributes, &attributes); err != nil {
			return nil, fmt.Errorf("decode log attributes: %w", err)
		}
		result = append(result, Entry{ID: row.ID, OccurredAt: row.OccurredAt.Time.UTC(), Service: row.Service, Module: row.Module, Level: row.Level, Event: row.Event, RequestID: row.RequestID.String, Attributes: attributes})
	}
	return result, nil
}

func (store *Store) Cleanup(ctx context.Context, now time.Time) (int64, error) {
	deleted, err := store.queries.DeleteExpiredLogEntries(ctx, timestamp(now.UTC().Add(-DefaultRetention)))
	if err != nil {
		return 0, fmt.Errorf("expire log entries: %w", err)
	}
	return deleted, nil
}

func (store *Store) SetDebug(ctx context.Context, now time.Time, duration time.Duration) (*time.Time, error) {
	if duration < 0 || duration > MaxDebugDuration {
		return nil, ErrInvalidQuery
	}
	value := pgtype.Timestamptz{}
	var until *time.Time
	if duration > 0 {
		candidate := now.UTC().Add(duration)
		value = timestamp(candidate)
		until = &candidate
	}
	if _, err := store.queries.SetLogDebugLease(ctx, dbgen.SetLogDebugLeaseParams{EnabledUntil: value, UpdatedAt: timestamp(now.UTC())}); err != nil {
		return nil, fmt.Errorf("set debug lease: %w", err)
	}
	return until, nil
}

func (store *Store) DebugUntil(ctx context.Context) (*time.Time, error) {
	value, err := store.queries.GetLogDebugLease(ctx)
	if err != nil {
		return nil, fmt.Errorf("load debug lease: %w", err)
	}
	if !value.Valid {
		return nil, nil
	}
	until := value.Time.UTC()
	return &until, nil
}

func prepareEntry(entry Entry) (Entry, []byte, error) {
	entry.Service = strings.ToLower(strings.TrimSpace(entry.Service))
	entry.Module = strings.ToLower(strings.TrimSpace(entry.Module))
	entry.Level = strings.ToLower(strings.TrimSpace(entry.Level))
	entry.Event = strings.ToLower(strings.TrimSpace(entry.Event))
	if entry.OccurredAt.IsZero() || !identifierPattern.MatchString(entry.Service) || !identifierPattern.MatchString(entry.Module) || !eventPattern.MatchString(entry.Event) || !validLevel(entry.Level) || (entry.RequestID != "" && !requestIDPattern.MatchString(entry.RequestID)) {
		return Entry{}, nil, ErrInvalidEntry
	}
	entry.Attributes = Redact(entry.Attributes)
	attributes, err := json.Marshal(entry.Attributes)
	if err != nil || len(attributes) > MaxAttributesBytes {
		return Entry{}, nil, ErrInvalidEntry
	}
	return entry, attributes, nil
}

func validateQuery(query Query) error {
	if query.From.IsZero() || query.Until.IsZero() || !query.From.Before(query.Until) || query.Until.Sub(query.From) > 31*24*time.Hour {
		return ErrInvalidQuery
	}
	if query.Limit < 0 || query.Limit > MaxQueryLimit {
		return ErrInvalidQuery
	}
	if query.Service != "" && !identifierPattern.MatchString(query.Service) {
		return ErrInvalidQuery
	}
	if query.Module != "" && !identifierPattern.MatchString(query.Module) {
		return ErrInvalidQuery
	}
	if query.Level != "" && !validLevel(query.Level) {
		return ErrInvalidQuery
	}
	if query.Event != "" && !eventPattern.MatchString(query.Event) {
		return ErrInvalidQuery
	}
	if query.RequestID != "" && !requestIDPattern.MatchString(query.RequestID) {
		return ErrInvalidQuery
	}
	return nil
}

func validLevel(level string) bool {
	return level == "debug" || level == "info" || level == "warning" || level == "error"
}
func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
func optionalText(value string) pgtype.Text { return pgtype.Text{String: value, Valid: value != ""} }
