package queue

import (
	"context"
	"errors"
	"testing"
	"time"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/repo/sqlite"
)

func setupQueueTestDB(t *testing.T) {
	t.Helper()
	projectRoot := t.TempDir()
	if err := sqlite.Init(projectRoot); err != nil {
		t.Fatalf("sqlite.Init failed: %v", err)
	}
	t.Cleanup(func() {
		sqlite.Close()
	})
}

func TestSqliteQueue_InterfaceContract(t *testing.T) {
	setupQueueTestDB(t)
	ctx := context.Background()

	// Verify domain.TaskQueue interface implementation
	var q domain.TaskQueue = NewSqliteQueue()

	task := domain.Task{
		ID:            "q_task_1",
		JobID:         "job_q",
		OwnerID:       "user_q",
		Step:          domain.StepNormalize,
		ResourceClass: domain.ResourceClassMedia,
		Priority:      5,
		MaxAttempts:   3,
	}

	if err := q.Enqueue(ctx, nil, task); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	claimed, err := q.Claim(ctx, "w1", []domain.ResourceClass{domain.ResourceClassMedia}, 2*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("Claim failed: %v, claimed: %+v", err, claimed)
	}
	if claimed.ID != "q_task_1" {
		t.Fatalf("unexpected claimed task ID: %s", claimed.ID)
	}

	// Heartbeat
	if err := q.Heartbeat(ctx, claimed.ID, "w1", 3*time.Second); err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}

	// Complete
	if err := q.Complete(ctx, nil, claimed.ID, "w1", claimed.Attempt); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// Attempting Complete again or with wrong attempt fails
	if err := q.Complete(ctx, nil, claimed.ID, "w1", claimed.Attempt); !errors.Is(err, sqlite.ErrFencingTokenMismatch) {
		t.Fatalf("expected ErrFencingTokenMismatch on duplicate complete, got: %v", err)
	}
}

