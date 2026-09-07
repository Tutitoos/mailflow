package sentry

import (
	"context"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
)

type TelemetrySummary struct {
	Traces         int64 `json:"traces"`
	Spans          int64 `json:"spans"`
	Profiles       int64 `json:"profiles"`
	Replays        int64 `json:"replays"`
	ReplaySegments int64 `json:"replaySegments"`
	ReplayEnabled  bool  `json:"replayEnabled"`
}

func (service *Service) TelemetrySummary(ctx context.Context, since time.Time) (TelemetrySummary, error) {
	if service.queries == nil || since.IsZero() {
		return TelemetrySummary{}, ErrUnavailable
	}
	row, err := service.queries.SentryTelemetrySummary(ctx, timestamp(since))
	if err != nil {
		return TelemetrySummary{}, err
	}
	return mapTelemetrySummary(row, service.config.ReplayEnabled), nil
}

func mapTelemetrySummary(row dbgen.SentryTelemetrySummaryRow, enabled bool) TelemetrySummary {
	return TelemetrySummary{
		Traces: row.Traces, Spans: row.Spans, Profiles: row.Profiles, Replays: row.Replays,
		ReplaySegments: row.ReplaySegments, ReplayEnabled: enabled,
	}
}
