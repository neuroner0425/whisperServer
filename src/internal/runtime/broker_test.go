package runtime_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	intruntime "whisperserver/src/internal/runtime"
)

func TestBrokerCoalescesJobProgress(t *testing.T) {
	broker := intruntime.NewBroker()
	ch := broker.Subscribe("user-1")
	defer broker.Unsubscribe("user-1", ch)

	// Rapidly emit 5 progress events within 50ms
	for i := 1; i <= 5; i++ {
		broker.Notify("user-1", "job.progress", map[string]any{
			"job_id":           "job-abc",
			"progress_percent": i * 20,
			"phase":            "전사 중",
		})
		time.Sleep(10 * time.Millisecond)
	}

	// At 80ms, no event should have fired yet due to the 250ms coalescing window
	select {
	case msg := <-ch:
		t.Fatalf("expected progress event to be coalesced/delayed, got immediate msg: %s", string(msg))
	case <-time.After(50 * time.Millisecond):
		// Expected: nothing received yet
	}

	// Wait for the coalescing window to complete (250ms from first event)
	select {
	case msg := <-ch:
		str := string(msg)
		if !strings.HasPrefix(str, "event: update\ndata: ") {
			t.Fatalf("malformed SSE wire format: %s", str)
		}
		jsonStr := strings.TrimPrefix(str, "event: update\ndata: ")
		jsonStr = strings.TrimSuffix(jsonStr, "\n\n")

		var payload map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			t.Fatalf("failed to unmarshal coalesced payload: %v", err)
		}

		if payload["type"] != "job.progress" {
			t.Fatalf("expected type job.progress, got: %v", payload["type"])
		}
		// Must reflect the latest state (100%)
		if percent, ok := payload["progress_percent"].(float64); !ok || int(percent) != 100 {
			t.Fatalf("expected latest progress 100, got: %v", payload["progress_percent"])
		}

	case <-time.After(400 * time.Millisecond):
		t.Fatal("timed out waiting for coalesced progress event")
	}

	// Ensure no extra duplicated events were queued
	select {
	case extra := <-ch:
		t.Fatalf("expected exactly 1 coalesced event, got extra: %s", string(extra))
	case <-time.After(100 * time.Millisecond):
		// Expected: no extra messages
	}
}

func TestBrokerDurableEventsImmediate(t *testing.T) {
	broker := intruntime.NewBroker()
	ch := broker.Subscribe("user-2")
	defer broker.Unsubscribe("user-2", ch)

	// Emit files.changed event
	start := time.Now()
	broker.Notify("user-2", "files.changed", map[string]any{"folder_id": "f-1"})

	select {
	case msg := <-ch:
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			t.Fatalf("durable event took too long to emit: %v", elapsed)
		}
		if !strings.Contains(string(msg), `"type":"files.changed"`) {
			t.Fatalf("unexpected message content: %s", string(msg))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for immediate durable event")
	}
}

