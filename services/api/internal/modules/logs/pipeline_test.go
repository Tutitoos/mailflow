package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestPipelineRedactsStdoutDisablesDebugAndBoundsBackpressure(t *testing.T) {
	var output bytes.Buffer
	pipeline, err := NewPipeline(&output, "api", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(pipeline)
	logger.Debug("hidden", "event", "mail.debug")
	logger.Info("safe message", "event", "mail.sent", "subject", "private", "error", "owner@example.test")
	var line map[string]any
	if err := json.Unmarshal(output.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	if line["subject"] != redacted || line["error"] != redacted || line["event"] != "mail.sent" || line["service"] != "api" {
		t.Fatalf("unsafe stdout: %#v", line)
	}
	if bytes.Contains(output.Bytes(), []byte("hidden")) || bytes.Contains(output.Bytes(), []byte("private")) || bytes.Contains(output.Bytes(), []byte("owner@")) {
		t.Fatalf("sensitive value reached stdout: %s", output.Bytes())
	}

	pipeline.Attach(&Store{})
	for range DefaultQueueSize + 1 {
		logger.Info("bounded", "event", "runtime.ready")
	}
	if pipeline.Dropped() == 0 {
		t.Fatal("full persistence queue did not drop without blocking")
	}
	if !pipeline.Enabled(context.Background(), slog.LevelInfo) || pipeline.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("unexpected default log levels")
	}
}
