package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	mailflowsync "github.com/Tutitoos/mailflow/services/api/internal/modules/sync"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/Tutitoos/mailflow/services/api/internal/platform/queue"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	QueueRetryAction  = "queue.retry_sync"
	MaxOperations     = 100
	MinIdempotencyKey = 16
	MaxIdempotencyKey = 128
	DefaultOperations = 25
)

var (
	ErrOperationsUnavailable = errors.New("admin operations unavailable")
	ErrInvalidOperation      = errors.New("invalid admin operation")
	ErrTargetNotFound        = errors.New("admin operation target not found")
)

type Synchronizer interface {
	Request(context.Context, string, string) (mailflowsync.Run, error)
}

type Operation struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Result    string    `json:"result"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type QueueOverview struct {
	Stats      queue.Stats `json:"stats"`
	Operations []Operation `json:"operations"`
}

type CDNStatus struct {
	AttachmentObjects int64 `json:"attachmentObjects"`
	AttachmentBytes   int64 `json:"attachmentBytes"`
	SentryObjects     int64 `json:"sentryObjects"`
	SentryBytes       int64 `json:"sentryBytes"`
	MissingObjects    int64 `json:"missingObjects"`
}

func (service *Service) QueueOverview(ctx context.Context, actorUserID string) (QueueOverview, error) {
	if service.queue == nil || service.pool == nil || !validUUID(actorUserID) {
		return QueueOverview{}, ErrOperationsUnavailable
	}
	stats, err := service.queue.Stats(ctx)
	if err != nil {
		return QueueOverview{}, fmt.Errorf("%w: queue stats", ErrOperationsUnavailable)
	}
	operations, err := service.ListOperations(ctx, actorUserID, DefaultOperations)
	if err != nil {
		return QueueOverview{}, err
	}
	return QueueOverview{Stats: stats, Operations: operations}, nil
}

func (service *Service) RetrySynchronization(ctx context.Context, synchronizer Synchronizer, actorUserID, accountID, idempotencyKey string) (Operation, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if service.pool == nil || synchronizer == nil || !validUUID(actorUserID) || !validUUID(accountID) || len(idempotencyKey) < MinIdempotencyKey || len(idempotencyKey) > MaxIdempotencyKey {
		return Operation{}, false, ErrInvalidOperation
	}
	operation, created, err := service.reserveOperation(ctx, actorUserID, accountID, idempotencyKey)
	if err != nil || !created {
		return operation, created, err
	}
	run, syncErr := synchronizer.Request(ctx, actorUserID, accountID)
	result := "queued"
	if syncErr == nil && run.State == mailflowsync.RunRunning {
		result = "already_running"
	}
	if syncErr != nil {
		result = "failed"
	}
	operation, updateErr := service.finishOperation(ctx, operation.ID, result)
	if updateErr != nil {
		return Operation{}, true, updateErr
	}
	if errors.Is(syncErr, mailflowsync.ErrInvalidRun) || errors.Is(syncErr, mailflowsync.ErrRunNotFound) {
		return operation, true, ErrTargetNotFound
	}
	if syncErr != nil {
		return operation, true, ErrOperationsUnavailable
	}
	return operation, true, nil
}

func (service *Service) ListOperations(ctx context.Context, actorUserID string, limit int) ([]Operation, error) {
	if service.pool == nil || !validUUID(actorUserID) {
		return nil, ErrOperationsUnavailable
	}
	if limit <= 0 {
		limit = DefaultOperations
	}
	if limit > MaxOperations {
		limit = MaxOperations
	}
	rows, err := service.pool.Query(ctx, `select id::text, action, result, created_at, updated_at
		from admin_operations where actor_user_id=$1::uuid order by created_at desc limit $2`, actorUserID, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: list operations", ErrOperationsUnavailable)
	}
	defer rows.Close()
	result := make([]Operation, 0, limit)
	for rows.Next() {
		var operation Operation
		if err := rows.Scan(&operation.ID, &operation.Action, &operation.Result, &operation.CreatedAt, &operation.UpdatedAt); err != nil {
			return nil, fmt.Errorf("%w: scan operation", ErrOperationsUnavailable)
		}
		operation.CreatedAt = operation.CreatedAt.UTC()
		operation.UpdatedAt = operation.UpdatedAt.UTC()
		result = append(result, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate operations", ErrOperationsUnavailable)
	}
	return result, nil
}

func (service *Service) CDNStatus(ctx context.Context) (CDNStatus, error) {
	if service.pool == nil {
		return CDNStatus{}, ErrOperationsUnavailable
	}
	rows, err := service.pool.Query(ctx, `select namespace, storage_status, count(*), coalesce(sum(size_bytes),0)
		from cdn_objects group by namespace, storage_status`)
	if err != nil {
		return CDNStatus{}, fmt.Errorf("%w: CDN status", ErrOperationsUnavailable)
	}
	defer rows.Close()
	var result CDNStatus
	for rows.Next() {
		var namespace, storageStatus string
		var count, size int64
		if err := rows.Scan(&namespace, &storageStatus, &count, &size); err != nil {
			return CDNStatus{}, fmt.Errorf("%w: scan CDN status", ErrOperationsUnavailable)
		}
		if storageStatus != "cached" {
			result.MissingObjects += count
			continue
		}
		if namespace == "attachments" {
			result.AttachmentObjects += count
			result.AttachmentBytes += size
		} else if namespace == "sentry" {
			result.SentryObjects += count
			result.SentryBytes += size
		}
	}
	if err := rows.Err(); err != nil {
		return CDNStatus{}, fmt.Errorf("%w: iterate CDN status", ErrOperationsUnavailable)
	}
	return result, nil
}

func (service *Service) reserveOperation(ctx context.Context, actorUserID, accountID, idempotencyKey string) (Operation, bool, error) {
	operationID, err := ids.New()
	if err != nil {
		return Operation{}, false, ErrOperationsUnavailable
	}
	targetHash := hashValue(accountID)
	keyHash := hashValue(idempotencyKey)
	var operation Operation
	err = service.pool.QueryRow(ctx, `insert into admin_operations
		(id, actor_user_id, action, target_hash, idempotency_key_hash, result)
		values ($1::uuid,$2::uuid,$3,$4,$5,'requested')
		on conflict (actor_user_id, action, idempotency_key_hash) do nothing
		returning id::text, action, result, created_at, updated_at`,
		operationID, actorUserID, QueueRetryAction, targetHash, keyHash,
	).Scan(&operation.ID, &operation.Action, &operation.Result, &operation.CreatedAt, &operation.UpdatedAt)
	if err == nil {
		return operation, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, false, fmt.Errorf("%w: reserve operation", ErrOperationsUnavailable)
	}
	err = service.pool.QueryRow(ctx, `select id::text, action, result, created_at, updated_at
		from admin_operations where actor_user_id=$1::uuid and action=$2 and idempotency_key_hash=$3`,
		actorUserID, QueueRetryAction, keyHash,
	).Scan(&operation.ID, &operation.Action, &operation.Result, &operation.CreatedAt, &operation.UpdatedAt)
	if err != nil {
		return Operation{}, false, fmt.Errorf("%w: read operation", ErrOperationsUnavailable)
	}
	return operation, false, nil
}

func (service *Service) finishOperation(ctx context.Context, operationID, result string) (Operation, error) {
	var operation Operation
	err := service.pool.QueryRow(ctx, `update admin_operations set result=$2, updated_at=now()
		where id=$1::uuid returning id::text, action, result, created_at, updated_at`,
		operationID, result,
	).Scan(&operation.ID, &operation.Action, &operation.Result, &operation.CreatedAt, &operation.UpdatedAt)
	if err != nil {
		return Operation{}, fmt.Errorf("%w: finish operation", ErrOperationsUnavailable)
	}
	return operation, nil
}

func hashValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}
