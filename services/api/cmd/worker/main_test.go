package main

import (
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
