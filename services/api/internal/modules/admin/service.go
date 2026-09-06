package admin

import (
	"runtime"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/metrics"
)

type Component struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Status struct {
	State      string      `json:"state"`
	Version    string      `json:"version"`
	GoVersion  string      `json:"goVersion"`
	CheckedAt  time.Time   `json:"checkedAt"`
	Components []Component `json:"components"`
}

type Service struct {
	version string
	metrics *metrics.Registry
}

func NewService(version string, registry *metrics.Registry) *Service {
	return &Service{version: version, metrics: registry}
}

func (s *Service) Status() Status {
	return Status{
		State: "operational", Version: s.version, GoVersion: runtime.Version(), CheckedAt: time.Now().UTC(),
		Components: []Component{{Name: "api", Status: "operational"}, {Name: "worker", Status: "unknown"}, {Name: "postgres", Status: "unknown"}, {Name: "redis", Status: "unknown"}},
	}
}

func (s *Service) Metrics() []metrics.Point { return s.metrics.Snapshot() }
