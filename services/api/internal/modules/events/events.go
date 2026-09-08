package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

const EnvelopeVersion = 1

var (
	ErrCursorExpired = errors.New("event cursor expired")
	ErrInvalidCursor = errors.New("invalid event cursor")
	ErrInvalidEvent  = errors.New("invalid event")
)

type Envelope struct {
	Version   int             `json:"version"`
	Cursor    string          `json:"cursor"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type Config struct {
	Prefix          string
	MaxEvents       int64
	ReplayLimit     int64
	Retention       time.Duration
	ReadBlock       time.Duration
	MaxPayloadBytes int
}

func DefaultConfig() Config {
	return Config{Prefix: "mailflow", MaxEvents: 2_000, ReplayLimit: 500, Retention: 24 * time.Hour, ReadBlock: 5 * time.Second, MaxPayloadBytes: 32 << 10}
}

type Store struct {
	client redis.UniversalClient
	config Config
}

func NewStore(ctx context.Context, client redis.UniversalClient, config Config) (*Store, error) {
	if config.Prefix == "" || config.MaxEvents < 1 || config.ReplayLimit < 1 || config.Retention <= 0 || config.ReadBlock <= 0 || config.MaxPayloadBytes < 2 {
		return nil, fmt.Errorf("configure event store: %w", ErrInvalidEvent)
	}
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect event store: %w", err)
	}
	return &Store{client: client, config: config}, nil
}

func (store *Store) Publish(ctx context.Context, userID, eventType string, payload json.RawMessage) (Envelope, error) {
	if strings.TrimSpace(userID) == "" || !validType(eventType) || !safePayload(payload, store.config.MaxPayloadBytes) {
		return Envelope{}, ErrInvalidEvent
	}
	timestamp := time.Now().UTC()
	key := store.key(userID)
	cursor, err := store.client.XAdd(ctx, &redis.XAddArgs{
		Stream: key, MaxLen: store.config.MaxEvents, Approx: false,
		Values: []any{"version", EnvelopeVersion, "type", eventType, "timestamp", timestamp.Format(time.RFC3339Nano), "payload", string(payload)},
	}).Result()
	if err != nil {
		return Envelope{}, fmt.Errorf("publish event: %w", err)
	}
	if err := store.client.Expire(ctx, key, store.config.Retention).Err(); err != nil {
		return Envelope{}, fmt.Errorf("retain event stream: %w", err)
	}
	return Envelope{Version: EnvelopeVersion, Cursor: cursor, Type: eventType, Timestamp: timestamp, Payload: payload}, nil
}

func (store *Store) Replay(ctx context.Context, userID, cursor string) ([]Envelope, string, error) {
	key := store.key(userID)
	latest := "0-0"
	tail, err := store.client.XRevRangeN(ctx, key, "+", "-", 1).Result()
	if err != nil {
		return nil, latest, fmt.Errorf("read event tail: %w", err)
	}
	if len(tail) > 0 {
		latest = tail[0].ID
	}
	if cursor == "" {
		return nil, latest, nil
	}
	if !validCursor(cursor) {
		return nil, latest, ErrInvalidCursor
	}
	first, err := store.client.XRangeN(ctx, key, "-", "+", 1).Result()
	if err != nil {
		return nil, latest, fmt.Errorf("read event boundary: %w", err)
	}
	if len(first) == 0 || compareCursor(cursor, first[0].ID) < 0 {
		return nil, latest, ErrCursorExpired
	}
	messages, err := store.client.XRangeN(ctx, key, "("+cursor, "+", store.config.ReplayLimit+1).Result()
	if err != nil {
		return nil, latest, fmt.Errorf("replay events: %w", err)
	}
	if int64(len(messages)) > store.config.ReplayLimit {
		return nil, latest, ErrCursorExpired
	}
	events, err := decode(messages, store.config.MaxPayloadBytes)
	if err != nil {
		return nil, latest, err
	}
	if len(events) > 0 {
		latest = events[len(events)-1].Cursor
	}
	return events, latest, nil
}

func (store *Store) Next(ctx context.Context, userID, cursor string) ([]Envelope, error) {
	if !validCursor(cursor) {
		return nil, ErrInvalidCursor
	}
	streams, err := store.client.XRead(ctx, &redis.XReadArgs{Streams: []string{store.key(userID), cursor}, Count: 32, Block: store.config.ReadBlock}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read live events: %w", err)
	}
	if len(streams) == 0 {
		return nil, nil
	}
	return decode(streams[0].Messages, store.config.MaxPayloadBytes)
}

func (store *Store) key(userID string) string {
	digest := sha256.Sum256([]byte(userID))
	return store.config.Prefix + ":events:" + hex.EncodeToString(digest[:16])
}

func decode(messages []redis.XMessage, maxPayloadBytes int) ([]Envelope, error) {
	result := make([]Envelope, 0, len(messages))
	for _, message := range messages {
		version, err := strconv.Atoi(value(message, "version"))
		if err != nil || version != EnvelopeVersion {
			return nil, fmt.Errorf("decode event version: %w", ErrInvalidEvent)
		}
		timestamp, err := time.Parse(time.RFC3339Nano, value(message, "timestamp"))
		if err != nil {
			return nil, fmt.Errorf("decode event timestamp: %w", ErrInvalidEvent)
		}
		payload := json.RawMessage(value(message, "payload"))
		eventType := value(message, "type")
		if !validType(eventType) || !safePayload(payload, maxPayloadBytes) {
			return nil, fmt.Errorf("decode event payload: %w", ErrInvalidEvent)
		}
		result = append(result, Envelope{Version: version, Cursor: message.ID, Type: eventType, Timestamp: timestamp, Payload: payload})
	}
	return result, nil
}

func value(message redis.XMessage, key string) string {
	value, ok := message.Values[key]
	if !ok {
		return ""
	}
	return fmt.Sprint(value)
}

func validType(value string) bool {
	switch value {
	case "mail.changed", "sync.progress", "draft.changed", "admin.alert", "admin.log", "system.status", "translations.changed":
		return true
	default:
		return false
	}
}

func safePayload(payload json.RawMessage, limit int) bool {
	if len(payload) < 2 || len(payload) > limit || !json.Valid(payload) {
		return false
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return false
	}
	if value == nil {
		return false
	}
	return safeValue(value, 0)
}

func safeValue(value any, depth int) bool {
	if depth > 8 {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(key)
			for _, forbidden := range []string{"token", "cookie", "credential", "password", "subject", "body", "recipient", "email", "url"} {
				if strings.Contains(normalized, forbidden) {
					return false
				}
			}
			if !safeValue(child, depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range typed {
			if !safeValue(child, depth+1) {
				return false
			}
		}
	case string:
		normalized := strings.ToLower(typed)
		if len(typed) > 2<<10 || strings.Contains(typed, "@") || strings.Contains(normalized, "bearer ") || strings.HasPrefix(typed, "eyJ") {
			return false
		}
	}
	return true
}

func validCursor(cursor string) bool {
	parts := strings.Split(cursor, "-")
	if len(parts) != 2 {
		return false
	}
	_, first := strconv.ParseUint(parts[0], 10, 64)
	_, second := strconv.ParseUint(parts[1], 10, 64)
	return first == nil && second == nil
}

func compareCursor(left, right string) int {
	leftParts := strings.Split(left, "-")
	rightParts := strings.Split(right, "-")
	for index := range 2 {
		leftValue, _ := strconv.ParseUint(leftParts[index], 10, 64)
		rightValue, _ := strconv.ParseUint(rightParts[index], 10, 64)
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}
