package sentry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	"github.com/jackc/pgx/v5"
)

const (
	ArtifactProcessJobKind = "sentry.artifact.process"
	MaxArtifactBytes       = 20 << 20
)

var (
	ErrInvalidRelease  = errors.New("invalid sentry release")
	ErrInvalidArtifact = errors.New("invalid sentry artifact")
)

type JobEnqueuer interface {
	Enqueue(context.Context, string, json.RawMessage, queue.EnqueueOptions) (queue.Job, bool, error)
}

type Release struct {
	ID        int64     `json:"id"`
	Component string    `json:"component"`
	Version   string    `json:"version"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"dateCreated"`
}

type Artifact struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Checksum  string    `json:"sha256"`
	Size      int64     `json:"size"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"dateCreated"`
}

func (service *Service) SetQueue(jobs JobEnqueuer) { service.jobs = jobs }

func (service *Service) CreateRelease(ctx context.Context, token, component, version string, now time.Time) (Release, error) {
	if service.queries == nil {
		return Release{}, ErrUnavailable
	}
	component, err := service.authorizeProject(ctx, token, component)
	if err != nil {
		return Release{}, err
	}
	version = safeRelease(version)
	if version == "" {
		return Release{}, ErrInvalidRelease
	}
	row, err := service.queries.UpsertSentryRelease(ctx, dbgen.UpsertSentryReleaseParams{Component: component, Version: version, CreatedAt: timestamp(now)})
	if err != nil {
		return Release{}, fmt.Errorf("create sentry release: %w", err)
	}
	return mapRelease(row), nil
}

func (service *Service) UploadArtifact(ctx context.Context, token, component, version, name string, payload []byte, now time.Time) (Artifact, error) {
	if service.queries == nil || service.pool == nil || service.cdn == nil {
		return Artifact{}, ErrUnavailable
	}
	component, err := service.authorizeProject(ctx, token, component)
	if err != nil {
		return Artifact{}, err
	}
	version = safeRelease(version)
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	kind := artifactKind(name)
	if version == "" || kind == "" || name == "." || len(name) > 255 || len(payload) == 0 || len(payload) > MaxArtifactBytes {
		return Artifact{}, ErrInvalidArtifact
	}
	if kind == "sourcemap" {
		payload, err = normalizeSourceMap(payload)
		if err != nil {
			return Artifact{}, err
		}
	}
	release, err := service.queries.GetSentryRelease(ctx, dbgen.GetSentryReleaseParams{Component: component, Version: version})
	if errors.Is(err, pgx.ErrNoRows) {
		return Artifact{}, ErrInvalidRelease
	}
	if err != nil {
		return Artifact{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Artifact{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := service.queries.WithTx(tx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('mailflow:sentry:artifact-quota'))`); err != nil {
		return Artifact{}, err
	}
	stored, err := queries.SentryArtifactStoredBytes(ctx)
	if err != nil || int64(len(payload)) > service.config.ArtifactQuota || stored > service.config.ArtifactQuota-int64(len(payload)) {
		if err != nil {
			return Artifact{}, err
		}
		return Artifact{}, ErrStorageQuota
	}
	objectID, err := newEventID()
	if err != nil {
		return Artifact{}, err
	}
	info, err := service.cdn.PutValidated("sentry", objectID, "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		return Artifact{}, ErrInvalidArtifact
	}
	keepObject := false
	defer func() {
		if !keepObject {
			_ = service.cdn.Remove("sentry", objectID)
		}
	}()
	if err := queries.InsertSentryCDNObject(ctx, dbgen.InsertSentryCDNObjectParams{ObjectID: objectID, MediaType: info.MediaType, SizeBytes: info.Size, Etag: info.ETag, StoredAt: timestamp(now)}); err != nil {
		return Artifact{}, err
	}
	row, err := queries.InsertSentryArtifact(ctx, dbgen.InsertSentryArtifactParams{
		ReleaseID: release.ID, ObjectID: objectID, Name: name, Kind: kind,
		ChecksumSha256: info.ETag, SizeBytes: info.Size, CreatedAt: timestamp(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		existing, existingErr := service.queries.GetSentryArtifactByName(ctx, dbgen.GetSentryArtifactByNameParams{ReleaseID: release.ID, Name: name})
		if existingErr != nil {
			return Artifact{}, existingErr
		}
		artifact := Artifact{ID: existing.ID, Name: existing.Name, Kind: existing.Kind, Checksum: existing.ChecksumSha256, Size: existing.SizeBytes, Status: existing.Status, CreatedAt: existing.CreatedAt.Time.UTC()}
		return artifact, service.enqueueArtifact(ctx, artifact.ID)
	}
	if err != nil {
		return Artifact{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Artifact{}, err
	}
	keepObject = true
	artifact := mapArtifact(row)
	return artifact, service.enqueueArtifact(ctx, artifact.ID)
}

func (service *Service) authorizeProject(ctx context.Context, token, requested string) (string, error) {
	parts := strings.Fields(token)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		token = parts[1]
	}
	key := strings.ToLower(strings.TrimSpace(token))
	if !artifactTokenPattern.MatchString(key) {
		return "", ErrUnauthenticated
	}
	project, err := service.queries.GetSentryProjectByArtifactToken(ctx, optionalText(digest([]byte(key))))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthenticated
	}
	if err != nil {
		return "", err
	}
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested != "" && requested != project.Component {
		return "", ErrUnauthenticated
	}
	return project.Component, nil
}

func (service *Service) enqueueArtifact(ctx context.Context, artifactID int64) error {
	if service.jobs == nil {
		return ErrUnavailable
	}
	payload, _ := json.Marshal(map[string]int64{"artifactId": artifactID})
	_, _, err := service.jobs.Enqueue(ctx, ArtifactProcessJobKind, payload, queue.EnqueueOptions{IdempotencyKey: fmt.Sprintf("sentry-artifact:%d", artifactID), MaxAttempts: 5})
	return err
}

func artifactKind(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".map"):
		return "sourcemap"
	case strings.HasSuffix(lower, ".dsym"), strings.HasSuffix(lower, ".dsym.zip"):
		return "dsym"
	case strings.HasSuffix(lower, ".debug"), strings.HasSuffix(lower, ".sym"), strings.HasSuffix(lower, ".dif"):
		return "dif"
	default:
		return ""
	}
}

func normalizeSourceMap(payload []byte) ([]byte, error) {
	var sourceMap sourceMapDocument
	if json.Unmarshal(payload, &sourceMap) != nil || sourceMap.Version != 3 || sourceMap.Mappings == "" {
		return nil, ErrInvalidArtifact
	}
	sourceMap.File = path.Base(strings.ReplaceAll(sourceMap.File, "\\", "/"))
	if !safeFilenamePattern.MatchString(sourceMap.File) {
		sourceMap.File = ""
	}
	for index, source := range sourceMap.Sources {
		base := path.Base(strings.ReplaceAll(source, "\\", "/"))
		if !safeFilenamePattern.MatchString(base) {
			base = "source-" + fmt.Sprint(index)
		}
		sourceMap.Sources[index] = base
	}
	for index, name := range sourceMap.Names {
		sourceMap.Names[index] = safeSymbol(name)
	}
	normalized, err := json.Marshal(sourceMap)
	if err != nil {
		return nil, ErrInvalidArtifact
	}
	return normalized, nil
}

func mapRelease(row dbgen.SentryRelease) Release {
	return Release{ID: row.ID, Component: row.Component, Version: row.Version, Status: row.Status, CreatedAt: row.CreatedAt.Time.UTC()}
}

func mapArtifact(row dbgen.SentryReleaseArtifact) Artifact {
	return Artifact{ID: row.ID, Name: row.Name, Kind: row.Kind, Checksum: row.ChecksumSha256, Size: row.SizeBytes, Status: row.Status, CreatedAt: row.CreatedAt.Time.UTC()}
}
