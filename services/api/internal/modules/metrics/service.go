package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sort"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database/dbgen"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	MinuteRetention   = 30 * 24 * time.Hour
	HourRetention     = 365 * 24 * time.Hour
	DefaultQueryLimit = 2000
	MaxQueryLimit     = 5000
)

type Resolution string

const (
	Minute Resolution = "minute"
	Hour   Resolution = "hour"
	Day    Resolution = "day"
)

type Query struct {
	Resolution Resolution
	Name       string
	From       time.Time
	Until      time.Time
	Limit      int
}

type SeriesPoint struct {
	Bucket     time.Time         `json:"bucket"`
	Resolution Resolution        `json:"resolution"`
	Name       string            `json:"name"`
	Kind       Kind              `json:"kind"`
	Labels     map[string]string `json:"labels,omitempty"`
	Value      float64           `json:"value"`
	Count      int64             `json:"count"`
	Min        float64           `json:"min"`
	Max        float64           `json:"max"`
	P50        *float64          `json:"p50,omitempty"`
	P95        *float64          `json:"p95,omitempty"`
	P99        *float64          `json:"p99,omitempty"`
	Average    *float64          `json:"average,omitempty"`
	Rate       *float64          `json:"ratePerSecond,omitempty"`
}

type Service struct {
	registry *Registry
	pool     *pgxpool.Pool
	queries  *dbgen.Queries
	process  string
}

func NewService(registry *Registry, pool *pgxpool.Pool, process ...string) *Service {
	if registry == nil {
		registry = NewRegistry()
	}
	var queries *dbgen.Queries
	if pool != nil {
		queries = dbgen.New(pool)
	}
	name := ""
	if len(process) > 0 {
		name = process[0]
	}
	return &Service{registry: registry, pool: pool, queries: queries, process: name}
}

func (service *Service) Registry() *Registry { return service.registry }

func (service *Service) Flush(ctx context.Context, now time.Time) error {
	service.observeProcess()
	if service.pool == nil {
		return nil
	}
	batch := service.registry.Drain()
	if len(batch) == 0 {
		return nil
	}
	bucket := now.UTC().Truncate(time.Minute)
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		service.registry.Restore(batch)
		return fmt.Errorf("begin metric flush: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := service.queries.WithTx(tx)
	for _, point := range batch {
		dimensions, marshalErr := json.Marshal(point.Labels)
		if marshalErr != nil {
			service.registry.Restore(batch)
			return fmt.Errorf("encode metric dimensions: %w", marshalErr)
		}
		buckets := normalizedBuckets(point.Buckets)
		rows, upsertErr := queries.UpsertMetricMinute(ctx, dbgen.UpsertMetricMinuteParams{
			Bucket: timestamp(bucket), Name: point.Name, Kind: string(point.Kind), Dimensions: dimensions,
			Value: point.Value, Count: point.Count, MinValue: point.Min, MaxValue: point.Max,
			Histogram: buckets, UpdatedAt: timestamp(now.UTC()),
		})
		if upsertErr != nil || rows != 1 {
			service.registry.Restore(batch)
			if upsertErr == nil {
				upsertErr = ErrInvalidMetric
			}
			return fmt.Errorf("persist metric point: %w", upsertErr)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		service.registry.Restore(batch)
		return fmt.Errorf("commit metric flush: %w", err)
	}
	return nil
}

func (service *Service) observeProcess() {
	if service.process == "" {
		return
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	labels := map[string]string{"service": service.process, "module": "process"}
	_ = service.registry.Set("mailflow_process_goroutines", string(Gauge), float64(runtime.NumGoroutine()), labels)
	_ = service.registry.Set("mailflow_process_heap_bytes", string(Gauge), float64(memory.HeapAlloc), labels)
}

func (service *Service) Maintain(ctx context.Context, now time.Time) error {
	if service.pool == nil {
		return nil
	}
	now = now.UTC()
	completedHour := now.Truncate(time.Hour).Add(-time.Hour)
	if err := service.rollup(ctx, Minute, Hour, completedHour, completedHour.Add(time.Hour)); err != nil {
		return err
	}
	completedDay := now.Truncate(24 * time.Hour).Add(-24 * time.Hour)
	if err := service.rollup(ctx, Hour, Day, completedDay, completedDay.Add(24*time.Hour)); err != nil {
		return err
	}
	_, err := service.queries.DeleteExpiredMetricPoints(ctx, dbgen.DeleteExpiredMetricPointsParams{
		Bucket: timestamp(now.Add(-MinuteRetention)), Bucket_2: timestamp(now.Add(-HourRetention)),
	})
	if err != nil {
		return fmt.Errorf("expire metric points: %w", err)
	}
	return nil
}

func (service *Service) rollup(ctx context.Context, source, target Resolution, from, until time.Time) error {
	rows, err := service.queries.ListMetricPointsForRollup(ctx, dbgen.ListMetricPointsForRollupParams{
		Resolution: string(source), Bucket: timestamp(from), Bucket_2: timestamp(until),
	})
	if err != nil {
		return fmt.Errorf("load %s metric rollup: %w", target, err)
	}
	points := aggregateRows(rows)
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s metric rollup: %w", target, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := service.queries.WithTx(tx)
	for _, point := range points {
		if err := queries.ReplaceMetricPoint(ctx, dbgen.ReplaceMetricPointParams{
			Bucket: timestamp(from), Resolution: string(target), Name: point.Name, Kind: string(point.Kind),
			Dimensions: point.dimensions, Value: point.Value, Count: point.Count, MinValue: point.Min,
			MaxValue: point.Max, Histogram: normalizedBuckets(point.Buckets), UpdatedAt: timestamp(time.Now().UTC()),
		}); err != nil {
			return fmt.Errorf("persist %s metric rollup: %w", target, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s metric rollup: %w", target, err)
	}
	return nil
}

func (service *Service) Query(ctx context.Context, query Query) ([]SeriesPoint, error) {
	if query.Resolution != Minute && query.Resolution != Hour && query.Resolution != Day {
		return nil, ErrInvalidMetric
	}
	if query.Name != "" && !metricNamePattern.MatchString(query.Name) {
		return nil, ErrInvalidMetric
	}
	if query.From.IsZero() || query.Until.IsZero() || !query.From.Before(query.Until) {
		return nil, ErrInvalidMetric
	}
	maximumRange := map[Resolution]time.Duration{Minute: 31 * 24 * time.Hour, Hour: 366 * 24 * time.Hour, Day: 20 * 365 * 24 * time.Hour}[query.Resolution]
	if query.Until.Sub(query.From) > maximumRange {
		return nil, ErrInvalidMetric
	}
	if query.Limit == 0 {
		query.Limit = DefaultQueryLimit
	}
	if query.Limit < 1 || query.Limit > MaxQueryLimit {
		return nil, ErrInvalidMetric
	}
	if service.queries == nil {
		return service.memoryQuery(query), nil
	}
	rows, err := service.queries.ListMetricPoints(ctx, dbgen.ListMetricPointsParams{
		Resolution: string(query.Resolution), Bucket: timestamp(query.From.UTC()), Bucket_2: timestamp(query.Until.UTC()), Column4: query.Name, Limit: int32(query.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("query metric points: %w", err)
	}
	result := make([]SeriesPoint, 0, len(rows))
	for _, row := range rows {
		point, convertErr := seriesPoint(row)
		if convertErr != nil {
			return nil, convertErr
		}
		result = append(result, point)
	}
	return result, nil
}

func (service *Service) memoryQuery(query Query) []SeriesPoint {
	points := service.registry.Snapshot()
	result := make([]SeriesPoint, 0, len(points))
	for _, point := range points {
		if query.Name != "" && point.Name != query.Name {
			continue
		}
		if point.Timestamp.Before(query.From) || !point.Timestamp.Before(query.Until) {
			continue
		}
		result = append(result, pointToSeries(point.Timestamp.Truncate(time.Minute), query.Resolution, point, point.Buckets))
		if len(result) == query.Limit {
			break
		}
	}
	return result
}

func (service *Service) Run(ctx context.Context, observers ...func(error)) error {
	report := func(err error) {
		if err != nil && len(observers) > 0 && observers[0] != nil {
			observers[0](err)
		}
	}
	report(service.Maintain(ctx, time.Now().UTC()))
	flush := time.NewTicker(time.Minute)
	maintenance := time.NewTicker(time.Hour)
	defer flush.Stop()
	defer maintenance.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			report(service.Flush(shutdown, time.Now().UTC()))
			return ctx.Err()
		case now := <-flush.C:
			report(service.Flush(ctx, now))
		case now := <-maintenance.C:
			report(service.Maintain(ctx, now))
		}
	}
}

type aggregatePoint struct {
	Point
	dimensions []byte
	updated    time.Time
}

func aggregateRows(rows []dbgen.MetricPoint) []aggregatePoint {
	grouped := make(map[string]aggregatePoint)
	for _, row := range rows {
		key := row.Name + "\x00" + string(row.Dimensions)
		current, exists := grouped[key]
		kind := Kind(row.Kind)
		if !exists || (kind == Gauge && row.UpdatedAt.Time.After(current.updated)) {
			grouped[key] = aggregatePoint{Point: Point{Name: row.Name, Kind: kind, Value: row.Value, Count: row.Count, Min: row.MinValue, Max: row.MaxValue, Buckets: append([]int64(nil), row.Histogram...)}, dimensions: append([]byte(nil), row.Dimensions...), updated: row.UpdatedAt.Time}
			continue
		}
		if kind == Gauge {
			continue
		}
		current.Value += row.Value
		current.Count += row.Count
		current.Min = min(current.Min, row.MinValue)
		current.Max = max(current.Max, row.MaxValue)
		for index := range current.Buckets {
			current.Buckets[index] += row.Histogram[index]
		}
		grouped[key] = current
	}
	result := make([]aggregatePoint, 0, len(grouped))
	for _, point := range grouped {
		result = append(result, point)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name+string(result[i].dimensions) < result[j].Name+string(result[j].dimensions)
	})
	return result
}

func seriesPoint(row dbgen.MetricPoint) (SeriesPoint, error) {
	labels := make(map[string]string)
	if err := json.Unmarshal(row.Dimensions, &labels); err != nil {
		return SeriesPoint{}, fmt.Errorf("decode metric dimensions: %w", err)
	}
	point := Point{Name: row.Name, Kind: Kind(row.Kind), Value: row.Value, Count: row.Count, Min: row.MinValue, Max: row.MaxValue, Labels: labels}
	return pointToSeries(row.Bucket.Time, Resolution(row.Resolution), point, row.Histogram), nil
}

func pointToSeries(bucket time.Time, resolution Resolution, point Point, buckets []int64) SeriesPoint {
	result := SeriesPoint{Bucket: bucket.UTC(), Resolution: resolution, Name: point.Name, Kind: point.Kind, Labels: point.Labels, Value: point.Value, Count: point.Count, Min: point.Min, Max: point.Max}
	if point.Kind == Histogram && point.Count > 0 {
		p50, p95, p99 := percentile(buckets, point.Count, 0.50, point.Max), percentile(buckets, point.Count, 0.95, point.Max), percentile(buckets, point.Count, 0.99, point.Max)
		average := point.Value / float64(point.Count)
		result.P50, result.P95, result.P99 = &p50, &p95, &p99
		result.Average = &average
	}
	if point.Kind == Counter {
		seconds := map[Resolution]float64{Minute: 60, Hour: 3600, Day: 86400}[resolution]
		rate := point.Value / seconds
		result.Rate = &rate
	}
	return result
}

func percentile(buckets []int64, count int64, quantile, maximum float64) float64 {
	if count <= 0 {
		return 0
	}
	target := int64(float64(count)*quantile + 0.999999)
	seen := int64(0)
	for index, value := range buckets {
		seen += value
		if seen >= target {
			bound := histogramBounds[index]
			if math.IsInf(bound, 1) {
				return maximum
			}
			return bound
		}
	}
	return maximum
}

func normalizedBuckets(source []int64) []int64 {
	result := make([]int64, len(histogramBounds))
	copy(result, source)
	return result
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
