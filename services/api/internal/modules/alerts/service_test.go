package alerts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/platform/database"
	"github.com/Tutitoos/mailflow/services/api/internal/testkit"
)

type fakeSender struct {
	calls []Message
	err   error
}

func (sender *fakeSender) Send(_ context.Context, message Message) error {
	sender.calls = append(sender.calls, message)
	return sender.err
}

type fakeFallback struct {
	calls    int
	excluded string
}

func (sender *fakeFallback) Send(_ context.Context, _ Message, excluded string) error {
	sender.calls++
	sender.excluded = excluded
	return nil
}

func TestDeduplicationCooldownAndRecovery(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	smtpSender := &fakeSender{}
	service, err := NewService(pool, smtpSender, nil, Config{Cooldown: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	signal := Signal{Policy: "backup", Source: "backup", Code: "snapshot_failed", OperationalKey: "daily"}
	first, delivered, err := service.Trigger(ctx, signal)
	if err != nil || !delivered || len(smtpSender.calls) != 1 {
		t.Fatalf("first=%+v delivered=%v calls=%d err=%v", first, delivered, len(smtpSender.calls), err)
	}
	second, delivered, err := service.Trigger(ctx, signal)
	if err != nil || delivered || second.ID != first.ID || len(smtpSender.calls) != 1 {
		t.Fatalf("duplicate delivered=%v calls=%d err=%v", delivered, len(smtpSender.calls), err)
	}
	now = now.Add(2 * time.Hour)
	_, delivered, err = service.Trigger(ctx, signal)
	if err != nil || !delivered || len(smtpSender.calls) != 2 {
		t.Fatalf("reminder delivered=%v calls=%d err=%v", delivered, len(smtpSender.calls), err)
	}
	recovered, delivered, err := service.Recover(ctx, signal)
	if err != nil || !delivered || recovered.State != "recovered" || recovered.ID != first.ID || smtpSender.calls[2].Kind != "recovery" {
		t.Fatalf("recovery=%+v delivered=%v err=%v", recovered, delivered, err)
	}
}

func TestFallbackIsOptInAndCannotRecurse(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fallback := &fakeFallback{}
	smtpSender := &fakeSender{err: errors.New("down")}
	service, _ := NewService(pool, smtpSender, fallback, Config{Cooldown: time.Hour, FallbackEnabled: true})
	_, _, err = service.Trigger(ctx, Signal{Policy: "provider_auth", Source: "google", Code: "credentials_rejected", OperationalKey: "account-safe", FailingProvider: "google"})
	if err != nil {
		t.Fatal(err)
	}
	if fallback.calls != 1 || fallback.excluded != "google" {
		t.Fatal("fallback recursed through failing provider")
	}
	status, err := service.Status(ctx, 10)
	if err != nil || len(status.Deliveries) != 1 || status.Deliveries[0].Status != "sent" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestRejectsPrivateOperationalKeys(t *testing.T) {
	if validSignal(Signal{Policy: "backup", Source: "backup", Code: "failed", OperationalKey: "owner@example.test"}) {
		t.Fatal("accepted personal identifier")
	}
}

func TestPoliciesUseOnlyBoundedOperationalTokens(t *testing.T) {
	signals := Evaluate(Snapshot{ProviderAuthFailures: []string{"google", "owner@example.test"}, SyncRetryJobs: 100, DiskFreePercent: 9, BackupFailed: true, SentryUnavailable: true, UnhealthyServices: []string{"worker", "bad service"}})
	if len(signals) != 6 {
		t.Fatalf("signals=%+v", signals)
	}
	for _, signal := range signals {
		if !validSignal(signal) {
			t.Fatalf("unsafe signal=%+v", signal)
		}
	}
}

func TestReconcileSendsCorrelatedRecovery(t *testing.T) {
	databaseURL := testkit.PostgresDatabase(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	sender := &fakeSender{}
	service, _ := NewService(pool, sender, nil, Config{Cooldown: time.Hour})
	if err = service.Reconcile(ctx, Snapshot{SyncDeadJobs: 1, DiskFreePercent: 50}); err != nil {
		t.Fatal(err)
	}
	if err = service.Reconcile(ctx, Snapshot{DiskFreePercent: 50}); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, 10)
	if err != nil || len(status.Incidents) != 1 || status.Incidents[0].State != "recovered" || len(sender.calls) != 2 || sender.calls[1].IncidentID != sender.calls[0].IncidentID {
		t.Fatalf("status=%+v calls=%+v err=%v", status, sender.calls, err)
	}
}
