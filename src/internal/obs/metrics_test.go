package obs

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestMetricsRecording(t *testing.T) {
	RegisterMetrics()

	// 1. Job step duration
	ObserveJobStep("transcribe", "success", 1500*time.Millisecond)
	ObserveJobStep("refine", "failed", 500*time.Millisecond)

	// 2. Queue wait duration
	ObserveQueueWait("whisper", 250*time.Millisecond)
	ObserveQueueWait("gemini", 1200*time.Millisecond)

	// 3. Gemini metrics
	RecordGeminiRequest("gemini-2.5-flash", "200")
	RecordGeminiRequest("gemini-2.5-flash", "500")
	RecordGeminiTokens(100, 250)

	// 4. SSE Reloads
	IncSSEReload()

	// Verify GeminiRequestsTotal counter
	var m dto.Metric
	geminiReq, err := GeminiRequestsTotal.GetMetricWithLabelValues("gemini-2.5-flash", "200")
	if err != nil {
		t.Fatalf("failed to get metric: %v", err)
	}
	if err := geminiReq.Write(&m); err != nil {
		t.Fatalf("failed to write metric: %v", err)
	}
	if m.GetCounter().GetValue() < 1 {
		t.Fatalf("expected Gemini request count >= 1, got %v", m.GetCounter().GetValue())
	}

	// Verify SSEReloadsTotal
	var sseM dto.Metric
	if err := SSEReloadsTotal.Write(&sseM); err != nil {
		t.Fatalf("failed to write sse metric: %v", err)
	}
	if sseM.GetCounter().GetValue() < 1 {
		t.Fatalf("expected SSE reloads count >= 1, got %v", sseM.GetCounter().GetValue())
	}

	_ = prometheus.DefaultRegisterer
}

