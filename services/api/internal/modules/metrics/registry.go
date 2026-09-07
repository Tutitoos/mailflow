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
	key, err := pointKey(name, labels)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.points[key] = Point{Name: name, Kind: kind, Value: value, Labels: labels, Timestamp: time.Now().UTC()}
	return nil
}

func (r *Registry) Add(name string, value float64, labels map[string]string) error {
	key, err := pointKey(name, labels)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	point := r.points[key]
	point.Name, point.Kind, point.Labels = name, "counter", labels
	point.Value += value
	point.Timestamp = time.Now().UTC()
	r.points[key] = point
	return nil
}

func pointKey(name string, labels map[string]string) (string, error) {
	keys := make([]string, 0, len(labels))
	for label := range labels {
		if _, allowed := allowedDimensions[label]; !allowed {
			return "", fmt.Errorf("metric dimension %q is not allowed", label)
		}
		keys = append(keys, label)
	}
	sort.Strings(keys)
	key := name
	for _, label := range keys {
		key += "\x00" + label + "\x00" + labels[label]
	}
	return key, nil
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
