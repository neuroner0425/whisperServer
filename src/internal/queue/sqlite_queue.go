package queue

import (
	"context"
	"time"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/repo/sqlite"
)

// SqliteQueue implements domain.TaskQueue backed by SQLite durable storage.
type SqliteQueue struct{}

// NewSqliteQueue creates a new durable task queue backed by SQLite.
func NewSqliteQueue() *SqliteQueue {
	return &SqliteQueue{}
}

// Enqueue persists a task into the tasks table, optionally using an existing transaction.
func (q *SqliteQueue) Enqueue(ctx context.Context, tx domain.Tx, task domain.Task) error {
	return sqlite.EnqueueTask(ctx, tx, task)
}

// Claim attempts to lease an available or lease-expired task for the specified worker.
func (q *SqliteQueue) Claim(ctx context.Context, workerID string, classes []domain.ResourceClass, leaseDur time.Duration) (*domain.Task, error) {
	return sqlite.ClaimTask(ctx, workerID, classes, leaseDur)
}

// Heartbeat extends the lease of a currently held task.
func (q *SqliteQueue) Heartbeat(ctx context.Context, taskID, workerID string, leaseDur time.Duration) error {
	return sqlite.HeartbeatTask(ctx, taskID, workerID, leaseDur)
}

// Complete marks the task as completed, verifying the fencing token (workerID and attempt).
func (q *SqliteQueue) Complete(ctx context.Context, tx domain.Tx, taskID, workerID string, attempt int) error {
	return sqlite.CompleteTask(ctx, tx, taskID, workerID, attempt)
}

// Fail reschedules or dead-letters the task upon failure, verifying the fencing token.
func (q *SqliteQueue) Fail(ctx context.Context, tx domain.Tx, taskID, workerID string, attempt int, retryAfter *time.Duration, errMsg string) error {
	return sqlite.FailTask(ctx, tx, taskID, workerID, attempt, retryAfter, errMsg)
}

