package runtime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/repo/sqlite"
)

func setupOutboxTestDB(t *testing.T) {
	t.Helper()
	projectRoot := t.TempDir()
	if err := sqlite.Init(projectRoot); err != nil {
		t.Fatalf("sqlite.Init failed: %v", err)
	}
	t.Cleanup(func() {
		sqlite.Close()
	})
}

func TestOutboxRelay_DispatchesToBrokerAndIndex(t *testing.T) {
	setupOutboxTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	broker := NewBroker()
	var (
		indexMu       sync.Mutex
		indexedJobs   []string
		indexedOwners []string
	)
	mockIndexFn := func(jobID, ownerID string) error {
		indexMu.Lock()
		defer indexMu.Unlock()
		indexedJobs = append(indexedJobs, jobID)
		indexedOwners = append(indexedOwners, ownerID)
		return nil
	}

	relay := NewOutboxRelay(broker, mockIndexFn, t.Logf, func(scope string, err error, f string, args ...any) {
		t.Logf("ERR [%s]: %v", scope, err)
	})

	ownerID := "user_relay_1"
	jobID := "job_relay_1"

	// Subscribe to broker
	subCh := broker.Subscribe(ownerID)
	defer broker.Unsubscribe(ownerID, subCh)

	// 1. Insert index.requested event
	_, err := sqlite.InsertOutboxEvent(ctx, nil, domain.DomainEvent{
		OwnerID:     ownerID,
		JobID:       jobID,
		EventType:   domain.EventTypeIndexRequested,
		PayloadJSON: `{}`,
	})
	if err != nil {
		t.Fatalf("insert index event failed: %v", err)
	}

	// 2. Insert job.status event
	statusSeq, err := sqlite.InsertOutboxEvent(ctx, nil, domain.DomainEvent{
		OwnerID:     ownerID,
		JobID:       jobID,
		EventType:   domain.EventTypeJobStatus,
		PayloadJSON: `{"status_code":50,"status":"완료"}`,
	})
	if err != nil {
		t.Fatalf("insert status event failed: %v", err)
	}

	// Start relay
	relay.Start(ctx)
	defer relay.Stop()

	// Verify index function called
	for i := 0; i < 30; i++ {
		indexMu.Lock()
		n := len(indexedJobs)
		indexMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	indexMu.Lock()
	if len(indexedJobs) != 1 || indexedJobs[0] != jobID {
		t.Fatalf("expected indexed job %s, got %+v", jobID, indexedJobs)
	}
	indexMu.Unlock()

	// Verify SSE broadcast received with seq ID
	select {
	case msg := <-subCh:
		msgStr := string(msg)
		if !strings.Contains(msgStr, "id:") || !strings.Contains(msgStr, "job.status") {
			t.Fatalf("unexpected SSE message: %s", msgStr)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for SSE broker message")
	}

	// Verify outbox events marked published
	unpub, err := sqlite.FetchUnpublishedOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("FetchUnpublishedOutbox failed: %v", err)
	}
	for _, ev := range unpub {
		if ev.Seq == statusSeq {
			t.Fatalf("expected status event seq %d to be published, but still unpublished", statusSeq)
		}
	}
}

