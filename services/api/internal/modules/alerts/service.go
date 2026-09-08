package alerts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultCooldown = 30 * time.Minute
	DefaultLimit    = 50
	MaximumLimit    = 200
	Retention       = 90 * 24 * time.Hour
)

var (
	ErrInvalid     = errors.New("invalid operational alert")
	ErrUnavailable = errors.New("operational alerts unavailable")
	safeToken      = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
)

type Signal struct {
	Policy          string
	Source          string
	Code            string
	OperationalKey  string
	FailingProvider string
}

type Incident struct {
	ID            string     `json:"id"`
	Policy        string     `json:"policy"`
	Source        string     `json:"source"`
	Code          string     `json:"code"`
	State         string     `json:"state"`
	OpenedAt      time.Time  `json:"openedAt"`
	LastSeenAt    time.Time  `json:"lastSeenAt"`
	RecoveredAt   *time.Time `json:"recoveredAt,omitempty"`
	CooldownUntil time.Time  `json:"cooldownUntil"`
}

type Delivery struct {
	ID          string     `json:"id"`
	IncidentID  string     `json:"incidentId"`
	Kind        string     `json:"kind"`
	Channel     string     `json:"channel"`
	Status      string     `json:"status"`
	ErrorCode   string     `json:"errorCode,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

type Status struct {
	Configured bool       `json:"configured"`
	Incidents  []Incident `json:"incidents"`
	Deliveries []Delivery `json:"deliveries"`
}

type Message struct{ Kind, Policy, Source, Code, IncidentID string }
type Sender interface {
	Send(context.Context, Message) error
}
type FallbackSender interface {
	Send(context.Context, Message, string) error
}
type MetricRecorder interface {
	Add(string, float64, map[string]string) error
}
type Config struct {
	Cooldown        time.Duration
	FallbackEnabled bool
	OwnerUserID     string
	Service         string
}

type Service struct {
	pool     *pgxpool.Pool
	smtp     Sender
	fallback FallbackSender
	config   Config
	now      func() time.Time
	publish  func(context.Context, string, string, json.RawMessage) error
	owner    func(context.Context) (string, error)
	metrics  MetricRecorder
}

func NewService(pool *pgxpool.Pool, smtp Sender, fallback FallbackSender, config Config) (*Service, error) {
	if pool == nil || config.Cooldown <= 0 {
		return nil, ErrInvalid
	}
	if config.Service == "" {
		config.Service = "api"
	}
	return &Service{pool: pool, smtp: smtp, fallback: fallback, config: config, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (service *Service) SetPublisher(publish func(context.Context, string, string, json.RawMessage) error) {
	service.publish = publish
}

func (service *Service) SetOwnerResolver(resolve func(context.Context) (string, error)) {
	service.owner = resolve
}

func (service *Service) SetMetrics(recorder MetricRecorder) { service.metrics = recorder }

func (service *Service) Trigger(ctx context.Context, signal Signal) (Incident, bool, error) {
	if !validSignal(signal) {
		return Incident{}, false, ErrInvalid
	}
	now := service.now().UTC()
	hash := deduplicationHash(signal)
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Incident{}, false, fmt.Errorf("%w: begin incident", ErrUnavailable)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, hash); err != nil {
		return Incident{}, false, fmt.Errorf("%w: lock incident", ErrUnavailable)
	}
	incident, found, err := loadIncident(ctx, tx, hash)
	if err != nil {
		return Incident{}, false, err
	}
	kind := "incident"
	if signal.Policy == "test" {
		kind = "test"
	}
	if found && incident.State == "active" && now.Before(incident.CooldownUntil) {
		if _, err = tx.Exec(ctx, `update alert_incidents set last_seen_at=$2 where id=$1::uuid`, incident.ID, now); err != nil {
			return Incident{}, false, ErrUnavailable
		}
		incident.LastSeenAt = now
		if err = tx.Commit(ctx); err != nil {
			return Incident{}, false, ErrUnavailable
		}
		return incident, false, nil
	}
	if found {
		if incident.State == "active" {
			kind = "reminder"
		}
		_, err = tx.Exec(ctx, `update alert_incidents set state='active',opened_at=case when state='recovered' then $2 else opened_at end,last_seen_at=$2,recovered_at=null,cooldown_until=$3 where id=$1::uuid`, incident.ID, now, now.Add(service.config.Cooldown))
	} else {
		incident.ID, err = ids.New()
		if err == nil {
			_, err = tx.Exec(ctx, `insert into alert_incidents (id,deduplication_hash,policy,source,code,state,opened_at,last_seen_at,cooldown_until) values ($1::uuid,$2,$3,$4,$5,'active',$6,$6,$7)`, incident.ID, hash, signal.Policy, signal.Source, signal.Code, now, now.Add(service.config.Cooldown))
		}
	}
	if err != nil {
		return Incident{}, false, fmt.Errorf("%w: persist incident", ErrUnavailable)
	}
	if err = tx.Commit(ctx); err != nil {
		return Incident{}, false, ErrUnavailable
	}
	incident, _, err = service.find(ctx, hash)
	if err != nil {
		return Incident{}, false, err
	}
	return incident, true, service.deliver(ctx, incident, kind, signal.FailingProvider)
}

func (service *Service) Recover(ctx context.Context, signal Signal) (Incident, bool, error) {
	if !validSignal(signal) {
		return Incident{}, false, ErrInvalid
	}
	now := service.now().UTC()
	var incident Incident
	err := service.pool.QueryRow(ctx, `update alert_incidents set state='recovered',last_seen_at=$2,recovered_at=$2
		where deduplication_hash=$1 and state='active'
		returning id::text,policy,source,code,state,opened_at,last_seen_at,recovered_at,cooldown_until`, deduplicationHash(signal), now).Scan(&incident.ID, &incident.Policy, &incident.Source, &incident.Code, &incident.State, &incident.OpenedAt, &incident.LastSeenAt, &incident.RecoveredAt, &incident.CooldownUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, _, findErr := service.find(ctx, deduplicationHash(signal))
		return existing, false, findErr
	}
	if err != nil {
		return Incident{}, false, ErrUnavailable
	}
	return incident, true, service.deliver(ctx, incident, "recovery", signal.FailingProvider)
}

// Reconcile opens every incident represented by the current aggregate snapshot
// and recovers previously active monitored incidents that are no longer present.
func (service *Service) Reconcile(ctx context.Context, snapshot Snapshot) error {
	current := Evaluate(snapshot)
	active := make(map[string]struct{}, len(current))
	for _, signal := range current {
		active[signal.Policy+"\x00"+signal.Source+"\x00"+signal.Code] = struct{}{}
		if _, _, err := service.Trigger(ctx, signal); err != nil {
			return err
		}
	}
	rows, err := service.pool.Query(ctx, `select policy,source,code from alert_incidents where state='active' and policy <> 'test'`)
	if err != nil {
		return ErrUnavailable
	}
	type identity struct{ policy, source, code string }
	missing := make([]identity, 0)
	for rows.Next() {
		var item identity
		if rows.Scan(&item.policy, &item.source, &item.code) != nil {
			rows.Close()
			return ErrUnavailable
		}
		if _, ok := active[item.policy+"\x00"+item.source+"\x00"+item.code]; !ok {
			missing = append(missing, item)
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return ErrUnavailable
	}
	for _, item := range missing {
		signal := Signal{Policy: item.policy, Source: item.source, Code: item.code, OperationalKey: operationalKey(item.policy, item.source)}
		if _, _, err := service.Recover(ctx, signal); err != nil {
			return err
		}
	}
	return service.Prune(ctx)
}

func (service *Service) Test(ctx context.Context, key string) (Incident, error) {
	incident, _, err := service.Trigger(ctx, Signal{Policy: "test", Source: "admin", Code: "delivery_test", OperationalKey: key})
	return incident, err
}

func (service *Service) deliver(ctx context.Context, incident Incident, kind, failingProvider string) error {
	message := Message{Kind: kind, Policy: incident.Policy, Source: incident.Source, Code: incident.Code, IncidentID: incident.ID}
	deliveryID, idErr := ids.New()
	if idErr != nil {
		return ErrUnavailable
	}
	if _, err := service.pool.Exec(ctx, `insert into alert_deliveries (id,incident_id,kind,channel,status) values ($1::uuid,$2::uuid,$3,'smtp','pending')`, deliveryID, incident.ID, kind); err != nil {
		return fmt.Errorf("%w: reserve delivery", ErrUnavailable)
	}
	channel, status, errorCode := "smtp", "sent", ""
	err := errors.New("SMTP unavailable")
	if service.smtp != nil {
		err = service.smtp.Send(ctx, message)
	}
	if err != nil {
		status, errorCode = "failed", "smtp_rejected"
		if service.smtp == nil {
			errorCode = "smtp_unavailable"
		}
		if service.config.FallbackEnabled {
			channel = "connected_account"
			if service.fallback == nil {
				errorCode = "fallback_failed"
			} else if err = service.fallback.Send(ctx, message, failingProvider); err == nil {
				status, errorCode = "sent", ""
			} else {
				errorCode = "fallback_failed"
			}
		} else {
			errorCode = "fallback_disabled"
		}
	}
	completed := service.now().UTC()
	if _, err := service.pool.Exec(ctx, `update alert_deliveries set channel=$2,status=$3,error_code=nullif($4,''),completed_at=$5 where id=$1::uuid and status='pending'`, deliveryID, channel, status, errorCode, completed); err != nil {
		return fmt.Errorf("%w: finish delivery", ErrUnavailable)
	}
	if service.metrics != nil {
		_ = service.metrics.Add("mailflow_alert_deliveries_total", 1, map[string]string{"service": service.config.Service, "module": "alerts", "operation": incident.Policy + "_" + channel, "result": status})
	}
	ownerUserID := service.config.OwnerUserID
	if ownerUserID == "" && service.owner != nil {
		ownerUserID, _ = service.owner(ctx)
	}
	if service.publish != nil && ownerUserID != "" {
		payload, _ := json.Marshal(map[string]string{"incidentId": incident.ID, "policy": incident.Policy, "source": incident.Source, "code": incident.Code, "state": incident.State, "deliveryStatus": status})
		_ = service.publish(ctx, ownerUserID, "admin.alert", payload)
	}
	return nil
}

func (service *Service) Status(ctx context.Context, limit int) (Status, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaximumLimit {
		return Status{}, ErrInvalid
	}
	rows, err := service.pool.Query(ctx, `select id::text,policy,source,code,state,opened_at,last_seen_at,recovered_at,cooldown_until from alert_incidents order by last_seen_at desc,id desc limit $1`, limit)
	if err != nil {
		return Status{}, ErrUnavailable
	}
	defer rows.Close()
	result := Status{Configured: service.smtp != nil || (service.config.FallbackEnabled && service.fallback != nil), Incidents: []Incident{}, Deliveries: []Delivery{}}
	for rows.Next() {
		var item Incident
		if rows.Scan(&item.ID, &item.Policy, &item.Source, &item.Code, &item.State, &item.OpenedAt, &item.LastSeenAt, &item.RecoveredAt, &item.CooldownUntil) != nil {
			return Status{}, ErrUnavailable
		}
		result.Incidents = append(result.Incidents, item)
	}
	rows.Close()
	deliveryRows, err := service.pool.Query(ctx, `select id::text,incident_id::text,kind,channel,status,coalesce(error_code,''),created_at,completed_at from alert_deliveries order by created_at desc,id desc limit $1`, limit)
	if err != nil {
		return Status{}, ErrUnavailable
	}
	defer deliveryRows.Close()
	for deliveryRows.Next() {
		var item Delivery
		if deliveryRows.Scan(&item.ID, &item.IncidentID, &item.Kind, &item.Channel, &item.Status, &item.ErrorCode, &item.CreatedAt, &item.CompletedAt) != nil {
			return Status{}, ErrUnavailable
		}
		result.Deliveries = append(result.Deliveries, item)
	}
	return result, deliveryRows.Err()
}

func (service *Service) Prune(ctx context.Context) error {
	_, err := service.pool.Exec(ctx, `delete from alert_incidents where last_seen_at < $1`, service.now().UTC().Add(-Retention))
	return err
}
func (service *Service) find(ctx context.Context, hash string) (Incident, bool, error) {
	return loadIncident(ctx, service.pool, hash)
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadIncident(ctx context.Context, query rowQuerier, hash string) (Incident, bool, error) {
	var i Incident
	err := query.QueryRow(ctx, `select id::text,policy,source,code,state,opened_at,last_seen_at,recovered_at,cooldown_until from alert_incidents where deduplication_hash=$1`, hash).Scan(&i.ID, &i.Policy, &i.Source, &i.Code, &i.State, &i.OpenedAt, &i.LastSeenAt, &i.RecoveredAt, &i.CooldownUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, false, nil
	}
	if err != nil {
		return Incident{}, false, ErrUnavailable
	}
	return i, true, nil
}

func validSignal(signal Signal) bool {
	return validPolicy(signal.Policy) && safeToken.MatchString(signal.Source) && safeToken.MatchString(signal.Code) && strings.TrimSpace(signal.OperationalKey) != "" && len(signal.OperationalKey) <= 256 && !strings.Contains(signal.OperationalKey, "@")
}
func validPolicy(value string) bool {
	switch value {
	case "provider_auth", "sync_backlog", "disk", "backup", "sentry_ingestion", "service_health", "test":
		return true
	}
	return false
}
func deduplicationHash(signal Signal) string {
	sum := sha256.Sum256([]byte(signal.Policy + "\x00" + signal.Source + "\x00" + signal.Code + "\x00" + signal.OperationalKey))
	return hex.EncodeToString(sum[:])
}

func operationalKey(policy, source string) string {
	switch policy {
	case "sync_backlog":
		return "queue"
	case "disk":
		return "cdn"
	case "backup":
		return "daily"
	case "sentry_ingestion":
		return "ingestion"
	}
	return source
}
