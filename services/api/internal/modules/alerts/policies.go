package alerts

const (
	SyncRetryThreshold = 100
	DiskFreeThreshold  = 10
)

// Snapshot contains aggregate, privacy-safe health facts. Collectors must not
// pass account IDs, addresses, message metadata, or arbitrary error strings.
type Snapshot struct {
	ProviderAuthFailures []string
	SyncRetryJobs        int64
	SyncDeadJobs         int64
	DiskFreePercent      int
	BackupFailed         bool
	BackupOverdue        bool
	SentryUnavailable    bool
	UnhealthyServices    []string
}

func Evaluate(snapshot Snapshot) []Signal {
	result := make([]Signal, 0, 8)
	for _, provider := range snapshot.ProviderAuthFailures {
		if safeToken.MatchString(provider) {
			result = append(result, Signal{Policy: "provider_auth", Source: provider, Code: "credentials_rejected", OperationalKey: provider, FailingProvider: provider})
		}
	}
	if snapshot.SyncRetryJobs >= SyncRetryThreshold || snapshot.SyncDeadJobs > 0 {
		result = append(result, Signal{Policy: "sync_backlog", Source: "worker", Code: "queue_requires_attention", OperationalKey: "queue"})
	}
	if snapshot.DiskFreePercent >= 0 && snapshot.DiskFreePercent < DiskFreeThreshold {
		result = append(result, Signal{Policy: "disk", Source: "cdn", Code: "capacity_low", OperationalKey: "cdn"})
	}
	if snapshot.BackupFailed {
		result = append(result, Signal{Policy: "backup", Source: "backup", Code: "last_run_failed", OperationalKey: "daily"})
	} else if snapshot.BackupOverdue {
		result = append(result, Signal{Policy: "backup", Source: "backup", Code: "snapshot_overdue", OperationalKey: "daily"})
	}
	if snapshot.SentryUnavailable {
		result = append(result, Signal{Policy: "sentry_ingestion", Source: "api", Code: "ingestion_unavailable", OperationalKey: "ingestion"})
	}
	for _, service := range snapshot.UnhealthyServices {
		if safeToken.MatchString(service) {
			result = append(result, Signal{Policy: "service_health", Source: service, Code: "service_unavailable", OperationalKey: service})
		}
	}
	return result
}
