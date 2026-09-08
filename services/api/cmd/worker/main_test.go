package main

import (
	"strings"
	"testing"
	"time"
)

func TestDurationFromEnv(t *testing.T) {
	t.Setenv("MAILFLOW_TEST_DURATION", "250ms")
	duration, err := durationFromEnv("MAILFLOW_TEST_DURATION", time.Second)
	if err != nil || duration != 250*time.Millisecond {
		t.Fatalf("unexpected duration: duration=%s err=%v", duration, err)
	}
	t.Setenv("MAILFLOW_TEST_DURATION", "0s")
	if _, err := durationFromEnv("MAILFLOW_TEST_DURATION", time.Second); err == nil {
		t.Fatal("expected zero duration to be rejected")
	}
}

func TestIMAPWatchConfigFromEnv(t *testing.T) {
	config, err := imapWatchConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.PollInterval != 5*time.Minute || config.Heartbeat != 25*time.Minute || config.MaxConnections != 4 {
		t.Fatalf("unexpected defaults: %+v", config)
	}
	t.Setenv("MAILFLOW_IMAP_POLL_INTERVAL", "7m")
	t.Setenv("MAILFLOW_IMAP_MAX_CONNECTIONS", "8")
	config, err = imapWatchConfigFromEnv()
	if err != nil || config.PollInterval != 7*time.Minute || config.MaxConnections != 8 {
		t.Fatalf("unexpected overrides: config=%+v err=%v", config, err)
	}
}

func TestIMAPWatchConfigRejectsUnsafeValues(t *testing.T) {
	t.Setenv("MAILFLOW_IMAP_MAX_CONNECTIONS", "65")
	if _, err := imapWatchConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "MAILFLOW_IMAP_MAX_CONNECTIONS") {
		t.Fatalf("maximum connection error=%v", err)
	}
	t.Setenv("MAILFLOW_IMAP_MAX_CONNECTIONS", "4")
	t.Setenv("MAILFLOW_IMAP_RECONNECT_MIN", "2m")
	if _, err := imapWatchConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "MAILFLOW_IMAP_RECONNECT_MAX") {
		t.Fatalf("reconnect range error=%v", err)
	}
}
