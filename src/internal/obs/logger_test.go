package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestJobLoggerJSONAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := InitLogger(&buf, slog.LevelInfo)

	jobLog := JobLogger("job-123", "user-456", "transcribe", 2)
	jobLog.Info("processing started", slog.String("detail", "test run"))

	output := buf.String()
	if !strings.Contains(output, `"job_id":"job-123"`) {
		t.Fatalf("expected job_id in log, got: %s", output)
	}
	if !strings.Contains(output, `"owner_id":"user-456"`) {
		t.Fatalf("expected owner_id in log, got: %s", output)
	}
	if !strings.Contains(output, `"step":"transcribe"`) {
		t.Fatalf("expected step in log, got: %s", output)
	}
	if !strings.Contains(output, `"attempt":2`) {
		t.Fatalf("expected attempt in log, got: %s", output)
	}
	if !strings.Contains(output, `"msg":"processing started"`) {
		t.Fatalf("expected msg in log, got: %s", output)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if parsed["level"] != "INFO" {
		t.Fatalf("expected level INFO, got %v", parsed["level"])
	}

	_ = logger
}

func TestContextWithJob(t *testing.T) {
	ctx := ContextWithJob(context.Background(), JobContext{
		JobID:   "ctx-job",
		OwnerID: "ctx-owner",
		Step:    "refine",
		Attempt: 1,
	})

	var buf bytes.Buffer
	InitLogger(&buf, slog.LevelInfo)

	ctxLogger := FromContext(ctx)
	ctxLogger.Info("hello from ctx")

	out := buf.String()
	if !strings.Contains(out, `"job_id":"ctx-job"`) {
		t.Fatalf("expected ctx-job in log, got %s", out)
	}
	if !strings.Contains(out, `"step":"refine"`) {
		t.Fatalf("expected step refine in log, got %s", out)
	}
}

