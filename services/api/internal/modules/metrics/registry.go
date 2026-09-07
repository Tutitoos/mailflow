package metrics

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	Counter          Kind = "counter"
	Gauge            Kind = "gauge"
	Histogram        Kind = "histogram"
	DefaultMaxSeries      = 2048
)

var (
	ErrInvalidMetric  = errors.New("invalid metric")
	ErrSeriesLimit    = errors.New("metric series limit reached")
	allowedDimensions = map[string]struct{}{"service": {}, "module": {}, "provider": {}, "operation": {}, "result": {}}
	metricNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,127}$`)
	labelValuePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	histogramBounds   = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, math.Inf(1)}
)

type Point struct {
	Name      string            `json:"name"`
	Kind      Kind              `json:"kind"`
	Value     float64           `json:"value"`
	Count     int64             `json:"count"`
	Min       float64           `json:"min"`
	Max       float64           `json:"max"`
	Buckets   []int64           `json:"buckets,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

type Registry struct {
	mu        sync.RWMutex
	points    map[string]Point
	kinds     map[string]Kind
	maxSeries int
	now       func() time.Time
}

func NewRegistry() *Registry {
	return &Registry{points: make(map[string]Point), kinds: make(map[string]Kind), maxSeries: DefaultMaxSeries, now: func() time.Time { return time.Now().UTC() }}
}

func (r *Registry) Set(name, kind string, value float64, labels map[string]string) error {
	if Kind(kind) != Gauge || !finite(value) {
		return ErrInvalidMetric
	}
	key, normalized, err := pointKey(name, labels)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defined, exists := r.kinds[key]
	if exists && defined != Gauge {
		return ErrInvalidMetric
	}
	if !exists && len(r.kinds) >= r.maxSeries {
		return ErrSeriesLimit
	}
	r.kinds[key] = Gauge
	now := r.now().UTC()
	r.points[key] = Point{Name: name, Kind: Gauge, Value: value, Count: 1, Min: value, Max: value, Labels: normalized, Timestamp: now}
	return nil
}

func (r *Registry) Add(name string, value float64, labels map[string]string) error {
	if !finite(value) || value < 0 {
		return ErrInvalidMetric
	}
	return r.record(name, Counter, value, labels)
}

func (r *Registry) Observe(name string, value float64, labels map[string]string) error {
	if !finite(value) || value < 0 {
		return ErrInvalidMetric
	}
	return r.record(name, Histogram, value, labels)
}

func (r *Registry) record(name string, kind Kind, value float64, labels map[string]string) error {
	key, normalized, err := pointKey(name, labels)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defined, known := r.kinds[key]
	if known && defined != kind {
		return ErrInvalidMetric
	}
	if !known && len(r.kinds) >= r.maxSeries {
		return ErrSeriesLimit
	}
	r.kinds[key] = kind
	point, exists := r.points[key]
	now := r.now().UTC()
	if !exists {
		point = Point{Name: name, Kind: kind, Labels: normalized, Min: value, Max: value, Timestamp: now}
		if kind == Histogram {
			point.Buckets = make([]int64, len(histogramBounds))
		}
	}
	point.Value += value
	point.Count++
	point.Min = min(point.Min, value)
	point.Max = max(point.Max, value)
	point.Timestamp = now
	if kind == Histogram {
		for index, bound := range histogramBounds {
			if value <= bound {
				point.Buckets[index]++
				break
			}
		}
	}
	r.points[key] = point
	return nil
}

func pointKey(name string, labels map[string]string) (string, map[string]string, error) {
	if !metricNamePattern.MatchString(name) || len(labels) > len(allowedDimensions) {
		return "", nil, ErrInvalidMetric
	}
	keys := make([]string, 0, len(labels))
	normalized := make(map[string]string, len(labels))
	for label, raw := range labels {
		if _, allowed := allowedDimensions[label]; !allowed {
			return "", nil, fmt.Errorf("%w: dimension %q is not allowed", ErrInvalidMetric, label)
		}
		value := strings.ToLower(strings.TrimSpace(raw))
		if !labelValuePattern.MatchString(value) {
			return "", nil, fmt.Errorf("%w: dimension %q has an unsafe value", ErrInvalidMetric, label)
		}
		keys = append(keys, label)
		normalized[label] = value
	}
	sort.Strings(keys)
	key := name
	for _, label := range keys {
		key += "\x00" + label + "\x00" + normalized[label]
	}
	return key, normalized, nil
}

func (r *Registry) Snapshot() []Point {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedPoints(r.points)
}

// Drain removes deltas atomically. Gauges remain so every minute retains their latest value.
func (r *Registry) Drain() []Point {
	r.mu.Lock()
	defer r.mu.Unlock()
	points := sortedPoints(r.points)
	for key, point := range r.points {
		if point.Kind != Gauge {
			delete(r.points, key)
		}
	}
	return points
}

// Restore merges a drained batch back after a failed database transaction.
func (r *Registry) Restore(points []Point) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, restored := range points {
		key, _, err := pointKey(restored.Name, restored.Labels)
		if err != nil {
			continue
		}
		current, exists := r.points[key]
		if defined, known := r.kinds[key]; known && defined != restored.Kind {
			continue
		}
		r.kinds[key] = restored.Kind
		if restored.Kind == Gauge {
			if !exists || restored.Timestamp.After(current.Timestamp) {
				r.points[key] = clonePoint(restored)
			}
			continue
		}
		if !exists {
			r.points[key] = clonePoint(restored)
			continue
		}
		current.Value += restored.Value
		current.Count += restored.Count
		current.Min = min(current.Min, restored.Min)
		current.Max = max(current.Max, restored.Max)
		for index := range current.Buckets {
			current.Buckets[index] += restored.Buckets[index]
		}
		r.points[key] = current
	}
}

func sortedPoints(source map[string]Point) []Point {
	points := make([]Point, 0, len(source))
	for _, point := range source {
		points = append(points, clonePoint(point))
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].Name == points[j].Name {
			return labelsKey(points[i].Labels) < labelsKey(points[j].Labels)
		}
		return points[i].Name < points[j].Name
	})
	return points
}

func clonePoint(point Point) Point {
	point.Labels = mapsClone(point.Labels)
	point.Buckets = append([]int64(nil), point.Buckets...)
	return point
}

func mapsClone(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func labelsKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result string
	for _, key := range keys {
		result += key + "=" + labels[key] + "\x00"
	}
	return result
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
