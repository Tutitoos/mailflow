package sentry

import (
	"context"
	"errors"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const MaxIssueQueryLimit = 500

var ErrIssueNotFound = errors.New("sentry issue not found")

type Issue struct {
	ID          string    `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	Component   string    `json:"component"`
	Environment string    `json:"environment"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	FirstSeenAt time.Time `json:"firstSeenAt"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
	EventCount  int64     `json:"eventCount"`
}

func (service *Service) ListIssues(ctx context.Context, status string, limit int) ([]Issue, error) {
	if service.queries == nil {
		return nil, ErrUnavailable
	}
	if status != "" && !validIssueStatus(status) || limit < 0 || limit > MaxIssueQueryLimit {
		return nil, ErrInvalidEnvelope
	}
	if limit == 0 {
		limit = 100
	}
	rows, err := service.queries.ListSentryIssues(ctx, dbgen.ListSentryIssuesParams{Status: status, QueryLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	issues := make([]Issue, 0, len(rows))
	for _, row := range rows {
		issues = append(issues, mapIssue(row))
	}
	return issues, nil
}

func (service *Service) SetIssueStatus(ctx context.Context, id, status string) (Issue, error) {
	if service.queries == nil {
		return Issue{}, ErrUnavailable
	}
	parsed, err := uuid.Parse(id)
	if err != nil || !validIssueStatus(status) {
		return Issue{}, ErrInvalidEnvelope
	}
	row, err := service.queries.SetSentryIssueStatus(ctx, dbgen.SetSentryIssueStatusParams{Status: status, ID: pgtype.UUID{Bytes: parsed, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return Issue{}, ErrIssueNotFound
	}
	if err != nil {
		return Issue{}, err
	}
	return mapIssue(row), nil
}

func validIssueStatus(status string) bool {
	return status == "unresolved" || status == "resolved" || status == "ignored"
}

func mapIssue(row dbgen.SentryIssue) Issue {
	return Issue{
		ID: uuid.UUID(row.ID.Bytes).String(), Fingerprint: row.Fingerprint,
		Component: row.Component, Environment: row.Environment, Title: row.Title, Status: row.Status,
		FirstSeenAt: row.FirstSeenAt.Time.UTC(), LastSeenAt: row.LastSeenAt.Time.UTC(), EventCount: row.EventCount,
	}
}
