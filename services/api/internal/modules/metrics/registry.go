package metrics

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

var allowedDimensions = map[string]struct{}{
	"service": {}, "module": {}, "provider": {}, "operation": {}, "result": {},
}

type Point struct {
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

type Registry struct {
	mu     sync.RWMutex
	points map[string]Point
}

func NewRegistry() *Registry { return &Registry{points: make(map[string]Point)} }

func (r *Registry) Set(name, kind string, value float64, labels map[string]string) error {
	for label := range labels {
		if _, allowed := allowedDimensions[label]; !allowed {
			return fmt.Errorf("metric dimension %q is not allowed", label)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.points[name] = Point{Name: name, Kind: kind, Value: value, Labels: labels, Timestamp: time.Now().UTC()}
	return nil
}

func (r *Registry) Snapshot() []Point {
	r.mu.RLock()
	defer r.mu.RUnlock()
	points := make([]Point, 0, len(r.points))
	for _, point := range r.points {
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Name < points[j].Name })
	return points
}
