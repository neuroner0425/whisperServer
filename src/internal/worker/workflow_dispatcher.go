package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/obs"
	"whisperserver/src/internal/repo/sqlite"
)

const (
	defaultLeaseDuration     = 60 * time.Second
	defaultHeartbeatInterval = 15 * time.Second
	defaultPollInterval      = 500 * time.Millisecond
)

// WorkflowDispatcher coordinates durable task execution from the SQLite task queue.
type WorkflowDispatcher struct {
	workerID      string
	taskQueue     domain.TaskQueue
	txManager     domain.TxManager
	classes       []domain.ResourceClass
	handlers      map[domain.StepType]domain.StepHandler
	logf          func(string, ...any)
	errf          func(string, error, string, ...any)
	stopCh        chan struct{}
	wg            sync.WaitGroup
	runningMu     sync.Mutex
	runningCancels map[string]context.CancelFunc
}

// NewWorkflowDispatcher creates a durable queue worker dispatcher.
func NewWorkflowDispatcher(
	workerID string,
	taskQueue domain.TaskQueue,
	txManager domain.TxManager,
	classes []domain.ResourceClass,
	logf func(string, ...any),
	errf func(string, error, string, ...any),
) *WorkflowDispatcher {
	if workerID == "" {
		workerID = fmt.Sprintf("worker-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	if len(classes) == 0 {
		classes = []domain.ResourceClass{
			domain.ResourceClassMedia,
			domain.ResourceClassSTT,
			domain.ResourceClassLLM,
			domain.ResourceClassIndex,
		}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if errf == nil {
		errf = func(string, error, string, ...any) {}
	}
	return &WorkflowDispatcher{
		workerID:       workerID,
		taskQueue:      taskQueue,
		txManager:      txManager,
		classes:        classes,
		handlers:       make(map[domain.StepType]domain.StepHandler),
		logf:           logf,
		errf:           errf,
		stopCh:         make(chan struct{}),
		runningCancels: make(map[string]context.CancelFunc),
	}
}

// RegisterStepHandler associates a step type with its execution handler.
func (d *WorkflowDispatcher) RegisterStepHandler(step domain.StepType, handler domain.StepHandler) {
	d.handlers[step] = handler
}

// Start begins the dispatching loop in a background goroutine.
func (d *WorkflowDispatcher) Start(ctx context.Context) {
	d.wg.Add(1)
	go d.dispatchLoop(ctx)
	d.logf("[DISPATCHER] started worker_id=%s classes=%v", d.workerID, d.classes)
}

// Stop signals the dispatcher loop to exit and waits for in-flight tasks.
func (d *WorkflowDispatcher) Stop() {
	close(d.stopCh)
	d.runningMu.Lock()
	for _, cancel := range d.runningCancels {
		cancel()
	}
	d.runningMu.Unlock()
	d.wg.Wait()
	d.logf("[DISPATCHER] stopped worker_id=%s", d.workerID)
}

func (d *WorkflowDispatcher) dispatchLoop(ctx context.Context) {
	defer d.wg.Done()
	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.pollAndExecute(ctx)
		}
	}
}

func (d *WorkflowDispatcher) pollAndExecute(ctx context.Context) {
	task, err := d.taskQueue.Claim(ctx, d.workerID, d.classes, defaultLeaseDuration)
	if err != nil {
		d.errf("dispatcher.claim", err, "worker_id=%s", d.workerID)
		return
	}
	if task == nil {
		return // No ready tasks
	}

	d.logf("[DISPATCHER] claimed task_id=%s job_id=%s step=%s attempt=%d", task.ID, task.JobID, task.Step, task.Attempt)

	// Execute task with lease heartbeating
	d.executeTask(ctx, task)
}

func (d *WorkflowDispatcher) executeTask(parentCtx context.Context, task *domain.Task) {
	taskCtx, cancel := context.WithCancel(parentCtx)
	taskCtx = obs.ContextWithJob(taskCtx, obs.JobContext{
		JobID:   task.JobID,
		OwnerID: task.OwnerID,
		Step:    string(task.Step),
		Attempt: task.Attempt,
	})

	d.runningMu.Lock()
	d.runningCancels[task.ID] = cancel
	d.runningMu.Unlock()

	defer func() {
		cancel()
		d.runningMu.Lock()
		delete(d.runningCancels, task.ID)
		d.runningMu.Unlock()
	}()

	// Start background heartbeat
	hbStop := make(chan struct{})
	hbWg := sync.WaitGroup{}
	hbWg.Add(1)
	go func() {
		defer hbWg.Done()
		hbTicker := time.NewTicker(defaultHeartbeatInterval)
		defer hbTicker.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-taskCtx.Done():
				return
			case <-hbTicker.C:
				if err := d.taskQueue.Heartbeat(taskCtx, task.ID, d.workerID, defaultLeaseDuration); err != nil {
					d.errf("dispatcher.heartbeat", err, "task_id=%s", task.ID)
					// If heartbeat fails because lease was lost, abort execution
					if errors.Is(err, sqlite.ErrTaskNotLeased) {
						cancel()
						return
					}
				}
			}
		}
	}()

	handler, exists := d.handlers[task.Step]
	if !exists {
		close(hbStop)
		hbWg.Wait()
		d.failTask(parentCtx, task, fmt.Errorf("no handler registered for step: %s", task.Step))
		return
	}

	startedAt := time.Now().UTC()
	// Record step attempt as running
	_ = sqlite.RecordJobStep(parentCtx, nil, domain.JobStep{
		JobID:     task.JobID,
		Step:      task.Step,
		Attempt:   task.Attempt,
		Status:    domain.StepStatusRunning,
		StartedAt: startedAt,
	})

	result, execErr := handler.Execute(taskCtx, task)

	// Stop heartbeating before completing
	close(hbStop)
	hbWg.Wait()

	finishedAt := time.Now().UTC()

	if execErr != nil {
		d.failTask(parentCtx, task, execErr)
		_ = sqlite.RecordJobStep(parentCtx, nil, domain.JobStep{
			JobID:        task.JobID,
			Step:         task.Step,
			Attempt:      task.Attempt,
			Status:       domain.StepStatusFailed,
			ErrorMessage: execErr.Error(),
			StartedAt:    startedAt,
			FinishedAt:   &finishedAt,
		})
		return
	}

	// Step succeeded: commit step completion, record provenance, enqueue next task or mark job complete
	d.completeStep(parentCtx, task, result, startedAt, finishedAt)
}

func (d *WorkflowDispatcher) completeStep(ctx context.Context, task *domain.Task, res domain.StepResult, startedAt, finishedAt time.Time) {
	tx, err := d.txManager.BeginTx(ctx)
	if err != nil {
		d.errf("dispatcher.beginTx", err, "task_id=%s", task.ID)
		return
	}
	defer tx.Rollback()

	// 1. Record step provenance
	stepRecord := domain.JobStep{
		JobID:         task.JobID,
		Step:          task.Step,
		Attempt:       task.Attempt,
		Status:        domain.StepStatusCompleted,
		ArtifactKey:   res.ArtifactKey,
		QualityReport: res.QualityReport,
		ParamsJSON:    res.ParamsJSON,
		StartedAt:     startedAt,
		FinishedAt:    &finishedAt,
	}
	if err := sqlite.RecordJobStep(ctx, tx, stepRecord); err != nil {
		d.errf("dispatcher.recordStep", err, "task_id=%s", task.ID)
		return
	}

	// 2. Complete current task verifying fencing token
	if err := d.taskQueue.Complete(ctx, tx, task.ID, d.workerID, task.Attempt); err != nil {
		d.errf("dispatcher.completeTask", err, "task_id=%s attempt=%d", task.ID, task.Attempt)
		return
	}

	// 3. Chain next task OR mark job completed
	if res.NextStep != nil {
		nextClass := domain.ResourceClassMedia
		if res.NextClass != nil {
			nextClass = *res.NextClass
		}
		nextTask := domain.Task{
			ID:            fmt.Sprintf("task_%s_%s_%d", task.JobID, *res.NextStep, time.Now().UnixNano()),
			JobID:         task.JobID,
			OwnerID:       task.OwnerID,
			Step:          *res.NextStep,
			ResourceClass: nextClass,
			Priority:      task.Priority,
			PayloadJSON:   res.NextPayload,
		}
		if err := d.taskQueue.Enqueue(ctx, tx, nextTask); err != nil {
			d.errf("dispatcher.enqueueNext", err, "job_id=%s next_step=%s", task.JobID, *res.NextStep)
			return
		}
		d.logf("[DISPATCHER] step complete: job_id=%s step=%s -> enqueued next_step=%s", task.JobID, task.Step, *res.NextStep)
	} else {
		// Entire pipeline completed: update jobs status_code = 50 and outbox
		nowTS := float64(time.Now().Unix())
		rawTx := sqlite.GetRawTx(tx)
		if rawTx != nil {
			_, err = rawTx.ExecContext(ctx, `
				UPDATE jobs
				SET status_code = 50,
				    completed_ts = ?,
				    version = version + 1
				WHERE id = ? AND owner_id = ?
			`, nowTS, task.JobID, task.OwnerID)
			if err != nil {
				d.errf("dispatcher.completeJob", err, "job_id=%s", task.JobID)
				return
			}
		}

		// Insert index.requested outbox event for atomic FTS indexing (ADR-007)
		_, _ = sqlite.InsertOutboxEvent(ctx, tx, domain.DomainEvent{
			OwnerID:     task.OwnerID,
			JobID:       task.JobID,
			EventType:   domain.EventTypeIndexRequested,
			PayloadJSON: fmt.Sprintf(`{"job_id":%q,"owner_id":%q}`, task.JobID, task.OwnerID),
		})

		// Insert job.status outbox event
		statusPayload, _ := json.Marshal(map[string]any{
			"type":        domain.EventTypeJobStatus,
			"job_id":      task.JobID,
			"status_code": 50,
			"status":      "완료",
		})
		_, _ = sqlite.InsertOutboxEvent(ctx, tx, domain.DomainEvent{
			OwnerID:     task.OwnerID,
			JobID:       task.JobID,
			EventType:   domain.EventTypeJobStatus,
			PayloadJSON: string(statusPayload),
		})
		d.logf("[DISPATCHER] pipeline finished: job_id=%s status=completed", task.JobID)
	}

	if err := tx.Commit(); err != nil {
		d.errf("dispatcher.commitComplete", err, "task_id=%s", task.ID)
	}
}

func (d *WorkflowDispatcher) failTask(ctx context.Context, task *domain.Task, execErr error) {
	d.errf("dispatcher.stepFailed", execErr, "job_id=%s step=%s attempt=%d", task.JobID, task.Step, task.Attempt)

	tx, err := d.txManager.BeginTx(ctx)
	if err != nil {
		d.errf("dispatcher.failBeginTx", err, "task_id=%s", task.ID)
		return
	}
	defer tx.Rollback()

	retryDelay := 10 * time.Second
	var retryPtr *time.Duration = &retryDelay

	// Check if this error is permanent (e.g. infected malware)
	isPermanent := errors.Is(execErr, ErrPermanentFailure)
	if isPermanent {
		retryPtr = nil
	}

	failErr := d.taskQueue.Fail(ctx, tx, task.ID, d.workerID, task.Attempt, retryPtr, execErr.Error())
	if failErr != nil {
		d.errf("dispatcher.failQueue", failErr, "task_id=%s", task.ID)
		return
	}

	// If failed permanently or dead-lettered, update job state
	if isPermanent || task.Attempt >= task.MaxAttempts {
		statusCode := 60
		statusName := "실패"
		if errors.Is(execErr, ErrInfectedMalware) {
			statusCode = domain.JobStatusQuarantinedInfectedCode
			statusName = "악성코드 검출로 차단됨"
		}

		rawTx := sqlite.GetRawTx(tx)
		if rawTx != nil {
			res, updateErr := rawTx.ExecContext(ctx, `
				UPDATE jobs
				SET status_code = ?,
				    version = version + 1
				WHERE id = ? AND owner_id = ?
			`, statusCode, task.JobID, task.OwnerID)
			if updateErr != nil {
				d.errf("dispatcher.failJobUpdate", updateErr, "job_id=%s", task.JobID)
			} else if rows, _ := res.RowsAffected(); rows == 0 {
				d.logf("[DISPATCHER] failJobUpdate: 0 rows affected for job_id=%s", task.JobID)
			}
		}

		statusPayload, _ := json.Marshal(map[string]any{
			"type":          domain.EventTypeJobStatus,
			"job_id":        task.JobID,
			"status_code":   statusCode,
			"status":        statusName,
			"error_message": execErr.Error(),
		})
		_, _ = sqlite.InsertOutboxEvent(ctx, tx, domain.DomainEvent{
			OwnerID:     task.OwnerID,
			JobID:       task.JobID,
			EventType:   domain.EventTypeJobStatus,
			PayloadJSON: string(statusPayload),
		})
	}

	if err := tx.Commit(); err != nil {
		d.errf("dispatcher.commitFail", err, "task_id=%s", task.ID)
	}
}

// Errors defining step execution outcomes.
var (
	ErrPermanentFailure = errors.New("permanent step failure, do not retry")
	ErrInfectedMalware  = fmt.Errorf("%w: infected malware detected", ErrPermanentFailure)
)
