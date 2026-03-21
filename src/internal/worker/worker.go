package worker

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"whisperserver/src/internal/model"
	"whisperserver/src/internal/store"
)

type Config struct {
	SplitTaskQueues       bool
	TmpFolder             string
	ModelDir              string
	WhisperCLI            string
	JobTimeoutSec         int
	ProgressRe            *regexp.Regexp
	StatusPending         string
	StatusRunning         string
	StatusRefiningPending string
	StatusRefining        string
	StatusCompleted       string
	StatusFailed          string
}

type Deps struct {
	GetJob               func(string) *model.Job
	SetJobFields         func(string, map[string]any)
	AppendJobPreviewLine func(string, string)
	HasGeminiConfigured  func() bool
	RefineTranscript     func(string, string) (string, error)
	UniqueStrings        func([]string) []string
	GetTagDescriptions   func(string, []string) (map[string]string, error)
	Logf                 func(string, ...any)
	Errf                 func(string, error, string, ...any)
	IncInProgress        func()
	DecInProgress        func()
	SetQueueLength       func(float64)
	IncJobsTotal         func(string)
	ObserveJobDuration   func(float64)
}

type taskType string

const (
	taskTypeTranscribe taskType = "transcribe"
	taskTypeRefine     taskType = "refine"
)

type task struct {
	jobID string
	kind  taskType
}

type Worker struct {
	cfg             Config
	deps            Deps
	taskQueue       chan task
	transcribeQueue chan task
	refineQueue     chan task
	once            sync.Once
	cancelMu        sync.Mutex
	cancelMap       map[string]context.CancelFunc
}

func New(cfg Config, deps Deps) *Worker {
	return &Worker{
		cfg:             cfg,
		deps:            deps,
		taskQueue:       make(chan task, 256),
		transcribeQueue: make(chan task, 256),
		refineQueue:     make(chan task, 256),
		cancelMap:       map[string]context.CancelFunc{},
	}
}

func (w *Worker) Start() {
	w.once.Do(func() {
		if w.cfg.SplitTaskQueues {
			w.deps.Logf("[WORKER] start mode=split")
			go w.transcribeWorkerLoop()
			go w.refineWorkerLoop()
		} else {
			w.deps.Logf("[WORKER] start mode=single")
			go w.workerLoop()
		}
	})
}

func (w *Worker) Close() {
	if w.cfg.SplitTaskQueues {
		close(w.transcribeQueue)
		close(w.refineQueue)
		return
	}
	close(w.taskQueue)
}

func (w *Worker) EnqueueTranscribe(jobID string) {
	t := task{jobID: jobID, kind: taskTypeTranscribe}
	if w.cfg.SplitTaskQueues {
		w.transcribeQueue <- t
		w.setQueueLen()
		return
	}
	w.taskQueue <- t
	w.setQueueLen()
}

func (w *Worker) EnqueueRefine(jobID string) {
	t := task{jobID: jobID, kind: taskTypeRefine}
	if w.cfg.SplitTaskQueues {
		w.refineQueue <- t
		w.setQueueLen()
		return
	}
	w.taskQueue <- t
	w.setQueueLen()
}

func (w *Worker) RequeuePending(jobs map[string]*model.Job) {
	for id, job := range jobs {
		if job == nil || job.IsTrashed {
			continue
		}
		switch job.Status {
		case w.cfg.StatusPending, w.cfg.StatusRunning:
			if _, err := os.Stat(filepath.Join(w.cfg.TmpFolder, id+".wav")); err == nil {
				w.EnqueueTranscribe(id)
			}
		case w.cfg.StatusRefiningPending, w.cfg.StatusRefining:
			if store.HasJobBlob(id, store.BlobKindTranscript) {
				w.EnqueueRefine(id)
			}
		}
	}
}

func (w *Worker) Cancel(jobID string) {
	w.cancelMu.Lock()
	cancel := w.cancelMap[jobID]
	w.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (w *Worker) setCancel(jobID string, cancel context.CancelFunc) {
	w.cancelMu.Lock()
	defer w.cancelMu.Unlock()
	if cancel == nil {
		delete(w.cancelMap, jobID)
		return
	}
	w.cancelMap[jobID] = cancel
}

func (w *Worker) setQueueLen() {
	if w.deps.SetQueueLength == nil {
		return
	}
	if w.cfg.SplitTaskQueues {
		w.deps.SetQueueLength(float64(len(w.transcribeQueue) + len(w.refineQueue)))
		return
	}
	w.deps.SetQueueLength(float64(len(w.taskQueue)))
}

func (w *Worker) workerLoop() {
	for t := range w.taskQueue {
		w.deps.Logf("[WORKER] dequeued mode=single job_id=%s kind=%s", t.jobID, t.kind)
		w.deps.IncInProgress()
		w.setQueueLen()
		w.processTask(t, false)
		w.deps.DecInProgress()
		w.setQueueLen()
	}
}

func (w *Worker) transcribeWorkerLoop() {
	for t := range w.transcribeQueue {
		w.deps.Logf("[WORKER] dequeued mode=transcribe job_id=%s kind=%s", t.jobID, t.kind)
		w.deps.IncInProgress()
		w.setQueueLen()
		w.processTask(t, true)
		w.deps.DecInProgress()
		w.setQueueLen()
	}
}

func (w *Worker) refineWorkerLoop() {
	for t := range w.refineQueue {
		w.deps.Logf("[WORKER] dequeued mode=refine job_id=%s kind=%s", t.jobID, t.kind)
		w.deps.IncInProgress()
		w.setQueueLen()
		w.processTask(t, true)
		w.deps.DecInProgress()
		w.setQueueLen()
	}
}
