package sentry

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/cdn"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrEnvelopeTooLarge  = errors.New("sentry envelope exceeds the configured limit")
	ErrInvalidEnvelope   = errors.New("invalid sentry envelope")
	ErrUnauthenticated   = errors.New("invalid sentry project key")
	ErrRateLimited       = errors.New("sentry ingestion rate exceeded")
	ErrStorageQuota      = errors.New("sentry storage quota exceeded")
	ErrUnavailable       = errors.New("sentry ingestion unavailable")
	ErrDuplicate         = errors.New("sentry event already received")
	eventIDPattern       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	artifactTokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	keyPattern           = regexp.MustCompile(`(?i)(?:^|[, ]+)sentry_key=([0-9a-f]{32})(?:[, ]|$)`)
)

const (
	DefaultMaxEnvelopeBytes = 5 << 20
	DefaultStorageQuota     = 1 << 30
	DefaultArtifactQuota    = 2 << 30
	DefaultReplayQuota      = 512 << 20
	DefaultRatePerMinute    = 120
	DefaultRetention        = 30 * 24 * time.Hour
	DefaultTraceRetention   = 7 * 24 * time.Hour
	DefaultReplayRetention  = 3 * 24 * time.Hour
	largePayloadThreshold   = 64 << 10
	maxEnvelopeItems        = 100
	MaxReplayItemBytes      = 1 << 20
)

var supportedItems = map[string]bool{
	"event": true, "transaction": true, "session": true, "sessions": true,
	"attachment": true, "client_report": true, "check_in": true,
	"profile": true, "profile_chunk": true, "replay_event": true, "replay_recording": true,
}

type Receipt struct {
	ID         string    `json:"id"`
	ReceivedAt time.Time `json:"receivedAt,omitempty"`
}

type Request struct {
	Authorization string
	QueryKey      string
	LegacyEventID string
	Body          []byte
	Legacy        bool
	Now           time.Time
}

type Project struct {
	Component     string
	PublicKey     string
	ArtifactToken string
}

type Config struct {
	MaxEnvelopeBytes  int
	StorageQuota      int64
	ArtifactQuota     int64
	ReplayQuota       int64
	RatePerMinute     int
	Retention         time.Duration
	TraceRetention    time.Duration
	ReplayRetention   time.Duration
	TraceSampleRate   float64
	ProfileSampleRate float64
	ReplaySampleRate  float64
	ReplayEnabled     bool
}

func DefaultConfig() Config {
	return Config{
		MaxEnvelopeBytes: DefaultMaxEnvelopeBytes, StorageQuota: DefaultStorageQuota,
		ArtifactQuota: DefaultArtifactQuota, ReplayQuota: DefaultReplayQuota,
		RatePerMinute: DefaultRatePerMinute, Retention: DefaultRetention,
		TraceRetention: DefaultTraceRetention, ReplayRetention: DefaultReplayRetention,
		TraceSampleRate: 0.1, ProfileSampleRate: 0.05, ReplaySampleRate: 0.05,
	}
}

func DerivedProjects(masterKey []byte) ([]Project, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("sentry projects require the 32-byte master key")
	}
	components := []string{"api", "web", "desktop", "ios"}
	projects := make([]Project, 0, len(components))
	for _, component := range components {
		publicMAC := hmac.New(sha256.New, masterKey)
		_, _ = publicMAC.Write([]byte("mailflow:sentry:dsn:" + component))
		artifactMAC := hmac.New(sha256.New, masterKey)
		_, _ = artifactMAC.Write([]byte("mailflow:sentry:artifact:" + component))
		projects = append(projects, Project{Component: component, PublicKey: hex.EncodeToString(publicMAC.Sum(nil)[:16]), ArtifactToken: hex.EncodeToString(artifactMAC.Sum(nil))})
	}
	return projects, nil
}

type Service struct {
	config  Config
	pool    *pgxpool.Pool
	queries *dbgen.Queries
	cdn     *cdn.Store
	jobs    JobEnqueuer
	mu      sync.Mutex
	rates   map[string]rateWindow
}

type rateWindow struct {
	minute int64
	count  int
}

func NewService(maxEnvelopeBytes int) *Service {
	config := DefaultConfig()
	config.MaxEnvelopeBytes = maxEnvelopeBytes
	return &Service{config: config, rates: make(map[string]rateWindow)}
}

func NewPersistentService(pool *pgxpool.Pool, store *cdn.Store, config Config) (*Service, error) {
	if pool == nil || store == nil || config.MaxEnvelopeBytes <= 0 || config.StorageQuota <= 0 || config.ArtifactQuota <= 0 || config.ReplayQuota <= 0 || config.RatePerMinute <= 0 || config.Retention <= 0 || config.TraceRetention <= 0 || config.ReplayRetention <= 0 || !validSampleRate(config.TraceSampleRate) || !validSampleRate(config.ProfileSampleRate) || !validSampleRate(config.ReplaySampleRate) {
		return nil, errors.New("sentry ingestion requires bounded database and CDN storage")
	}
	return &Service{config: config, pool: pool, queries: dbgen.New(pool), cdn: store, rates: make(map[string]rateWindow)}, nil
}

func validSampleRate(value float64) bool { return value >= 0 && value <= 1 }

func (service *Service) ConfigureProjects(ctx context.Context, projects []Project, now time.Time) error {
	if service.queries == nil {
		return errors.New("sentry persistence unavailable")
	}
	for _, project := range projects {
		component := strings.ToLower(strings.TrimSpace(project.Component))
		key := strings.ToLower(strings.TrimSpace(project.PublicKey))
		artifactToken := strings.ToLower(strings.TrimSpace(project.ArtifactToken))
		if !validComponent(component) || !eventIDPattern.MatchString(key) || !artifactTokenPattern.MatchString(artifactToken) {
			return ErrInvalidEnvelope
		}
		if err := service.queries.UpsertSentryProject(ctx, dbgen.UpsertSentryProjectParams{Component: component, PublicKey: key, ArtifactTokenHash: optionalText(digest([]byte(artifactToken))), UpdatedAt: timestamp(now)}); err != nil {
			return fmt.Errorf("configure sentry project: %w", err)
		}
	}
	return nil
}

func (service *Service) Ingest(ctx context.Context, request Request) (Receipt, error) {
	if len(request.Body) == 0 {
		return Receipt{}, ErrInvalidEnvelope
	}
	if len(request.Body) > service.config.MaxEnvelopeBytes {
		return Receipt{}, ErrEnvelopeTooLarge
	}
	if service.queries == nil {
		return Receipt{}, ErrUnavailable
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	key := authenticationKey(request.Authorization, request.QueryKey)
	project, err := service.queries.GetSentryProjectByKey(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Receipt{}, ErrUnauthenticated
		}
		return Receipt{}, fmt.Errorf("resolve sentry project: %w", err)
	}
	if !service.allow(key, now) {
		return Receipt{}, ErrRateLimited
	}
	parsed, err := parseRequest(request)
	if err != nil {
		return Receipt{}, err
	}
	if err := service.persist(ctx, project.Component, parsed, int64(len(request.Body)), now); err != nil {
		if errors.Is(err, ErrDuplicate) {
			return Receipt{ID: parsed.eventID, ReceivedAt: now}, nil
		}
		return Receipt{}, err
	}
	return Receipt{ID: parsed.eventID, ReceivedAt: now}, nil
}

func (service *Service) Cleanup(ctx context.Context, now time.Time) (int64, error) {
	if service.queries == nil {
		return 0, nil
	}
	traceBefore := timestamp(now.UTC().Add(-service.config.TraceRetention))
	deletedTraces, err := service.queries.DeleteExpiredSentryTraces(ctx, traceBefore)
	if err != nil {
		return 0, fmt.Errorf("delete expired Sentry traces: %w", err)
	}
	deletedProfiles, err := service.queries.DeleteExpiredSentryProfiles(ctx, traceBefore)
	if err != nil {
		return deletedTraces, fmt.Errorf("delete expired Sentry profiles: %w", err)
	}
	replayBefore := timestamp(now.UTC().Add(-service.config.ReplayRetention))
	replayObjects, err := service.queries.ListExpiredSentryReplayObjects(ctx, replayBefore)
	if err != nil {
		return deletedTraces + deletedProfiles, fmt.Errorf("list expired Sentry Replay objects: %w", err)
	}
	deletedReplaySegments, err := service.queries.DeleteExpiredSentryReplaySegments(ctx, replayBefore)
	if err != nil {
		return deletedTraces + deletedProfiles, fmt.Errorf("delete expired Sentry Replay segments: %w", err)
	}
	if err := service.queries.RefreshSentryReplaySegmentCounts(ctx); err != nil {
		return deletedTraces + deletedProfiles + deletedReplaySegments, fmt.Errorf("refresh Sentry Replay segment counts: %w", err)
	}
	deletedReplays, err := service.queries.DeleteExpiredSentryReplays(ctx, replayBefore)
	if err != nil {
		return deletedTraces + deletedProfiles + deletedReplaySegments, fmt.Errorf("delete expired Sentry Replays: %w", err)
	}
	for _, objectID := range replayObjects {
		if err := service.queries.DeleteSentryCDNObject(ctx, objectID); err != nil {
			return deletedTraces + deletedProfiles + deletedReplaySegments + deletedReplays, fmt.Errorf("delete expired Sentry Replay metadata: %w", err)
		}
		if err := service.cdn.Remove("sentry", objectID); err != nil {
			return deletedTraces + deletedProfiles + deletedReplaySegments + deletedReplays, err
		}
	}
	expiredBefore := timestamp(now.UTC().Add(-service.config.Retention))
	artifacts, err := service.queries.ListExpiredSentryArtifacts(ctx, expiredBefore)
	if err != nil {
		return 0, fmt.Errorf("list expired sentry artifacts: %w", err)
	}
	deletedArtifacts, err := service.queries.DeleteExpiredSentryArtifacts(ctx, expiredBefore)
	if err != nil {
		return 0, fmt.Errorf("delete expired sentry artifacts: %w", err)
	}
	for _, objectID := range artifacts {
		if err := service.queries.DeleteSentryCDNObject(ctx, objectID); err != nil {
			return deletedArtifacts, fmt.Errorf("delete expired sentry artifact metadata: %w", err)
		}
		if err := service.cdn.Remove("sentry", objectID); err != nil {
			return deletedArtifacts, err
		}
	}
	objects, err := service.queries.ListExpiredSentryObjects(ctx, expiredBefore)
	if err != nil {
		return 0, fmt.Errorf("list expired sentry objects: %w", err)
	}
	deleted, err := service.queries.DeleteExpiredSentryEvents(ctx, expiredBefore)
	if err != nil {
		return 0, fmt.Errorf("delete expired sentry events: %w", err)
	}
	for _, object := range objects {
		if !object.Valid {
			continue
		}
		if err := service.queries.DeleteSentryCDNObject(ctx, object.String); err != nil {
			return deleted, fmt.Errorf("delete expired sentry metadata: %w", err)
		}
		if err := service.cdn.Remove("sentry", object.String); err != nil {
			return deleted, err
		}
	}
	return deleted + deletedArtifacts + deletedTraces + deletedProfiles + deletedReplaySegments + deletedReplays, nil
}

func (service *Service) Run(ctx context.Context) error {
	_, _ = service.Cleanup(ctx, time.Now().UTC())
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			_, _ = service.Cleanup(ctx, now)
		}
	}
}

func (service *Service) allow(key string, now time.Time) bool {
	minute := now.Unix() / 60
	service.mu.Lock()
	defer service.mu.Unlock()
	window := service.rates[key]
	if window.minute != minute {
		window = rateWindow{minute: minute}
	}
	if window.count >= service.config.RatePerMinute {
		return false
	}
	window.count++
	service.rates[key] = window
	return true
}

func validComponent(component string) bool {
	return component == "api" || component == "web" || component == "desktop" || component == "ios"
}

func authenticationKey(header, query string) string {
	if key := strings.ToLower(strings.TrimSpace(query)); eventIDPattern.MatchString(key) {
		return key
	}
	match := keyPattern.FindStringSubmatch(header)
	if len(match) == 2 {
		return strings.ToLower(match[1])
	}
	return ""
}

func newEventID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func optionalText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func digest(payload []byte) string {
	value := sha256.Sum256(payload)
	return hex.EncodeToString(value[:])
}
