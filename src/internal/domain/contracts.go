package domain

import (
	"context"
	"time"
)

// Tx represents an active database transaction.
type Tx interface {
	Commit() error
	Rollback() error
}

// TxManager manages database transactions.
type TxManager interface {
	BeginTx(ctx context.Context) (Tx, error)
}

// TaskQueue defines durable queue operations for background workflows.
type TaskQueue interface {
	Enqueue(ctx context.Context, tx Tx, task Task) error
	Claim(ctx context.Context, workerID string, classes []ResourceClass, leaseDur time.Duration) (*Task, error)
	Heartbeat(ctx context.Context, taskID, workerID string, leaseDur time.Duration) error
	Complete(ctx context.Context, tx Tx, taskID, workerID string, attempt int) error
	Fail(ctx context.Context, tx Tx, taskID, workerID string, attempt int, retryAfter *time.Duration, errMsg string) error
}

// EventPublisher publishes domain events for real-time consumers (SSE, WebSocket, etc.).
type EventPublisher interface {
	Publish(ctx context.Context, userID string, evt DomainEvent) error
}

// ProgressPublisher publishes non-persisted volatile progress updates.
type ProgressPublisher interface {
	Report(jobID, ownerID string, p ProgressSnapshot)
}

// StepHandler handles execution of a single discrete pipeline step.
type StepHandler interface {
	Execute(ctx context.Context, task *Task) (StepResult, error)
}

// JobRepository provides owner-scoped persistence operations for jobs.
type JobRepository interface {
	GetJob(ctx context.Context, ownerID, jobID string) (*Job, error)
	SaveJob(ctx context.Context, tx Tx, job *Job) error
	UpdateStatus(ctx context.Context, tx Tx, ownerID, jobID string, statusCode int, statusDetail string) error
}

