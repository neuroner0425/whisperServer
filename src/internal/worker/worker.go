// worker.go contains the shared queue loops and audio/refine worker orchestration.
package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	model "whisperserver/src/internal/domain"
	intwhisper "whisperserver/src/internal/integrations/whisper"
	"whisperserver/src/internal/queue"
	"whisperserver/src/internal/service"
	intutil "whisperserver/src/internal/util"
)

type Config struct {
	SplitTaskQueues          bool
	TmpFolder                string
	ModelDir                 string
	WhisperCLI               string
	JobTimeoutSec            int
	PDFBatchTimeoutSec       int
	PDFMaxPages              int
	PDFMaxPagesPerRequest    int
	PDFMaxRenderedImageBytes int64
	ProgressRe               *regexp.Regexp
	StatusPending            string
	StatusRunning            string
	StatusRefiningPending    string
	StatusRefining           string
	StatusCompleted          string
	StatusFailed             string
}

type DocumentPageImage struct {
	PageIndex int
	MIMEType  string
	Data      []byte
}

type DocumentChunk struct {
	ChunkIndex  int
	TotalChunks int
	StartPage   int
	EndPage     int
	TotalPages  int
	Images      []DocumentPageImage
}

type Deps struct {
	GetJob                func(string) *model.Job
	SetJobFields          func(string, map[string]any)
	AppendJobPreviewLine  func(string, string)
	ReplaceJobPreviewText func(string, string)
	BlobSvc               *service.JobBlobService
	ConvertToWav          func(string, string) error
	WhisperRunner         interface {
		RunFromBlob(context.Context, string, []byte, *int) (intwhisper.RunResult, error)
	}
	HasGeminiConfigured     func() bool
	RefineTranscript        func(string, string) (string, error)
	CountPDFPages           func(string) (int, error)
	RenderPDFToJPEGs        func(string, string) ([]string, error)
	ExtractDocumentChunk    func(context.Context, DocumentChunk, string) ([]byte, error)
	BuildConsistencyContext func([]byte) (string, error)
	MergeDocumentJSON       func(...[]byte) ([]byte, error)
	RenderDocumentMarkdown  func([]byte) (string, error)
	UniqueStrings           func([]string) []string
	GetTagDescriptions      func(string, []string) (map[string]string, error)
	Logf                    func(string, ...any)
	Errf                    func(string, error, string, ...any)
	IncInProgress           func()
	DecInProgress           func()
	SetQueueLength          func(float64)
	IncJobsTotal            func(string)
	ObserveJobDuration      func(float64)
}

type Worker struct {
	cfg             Config
	deps            Deps
	taskQueue       queue.Queue
	transcribeQueue queue.Queue
	refineQueue     queue.Queue
	once            sync.Once
	ctx             context.Context
	cancel          context.CancelFunc
	cancelMu        sync.Mutex
	cancelMap       map[string]context.CancelFunc
}

// New builds the worker with its queues and cancellation bookkeeping.
func New(cfg Config, deps Deps) *Worker {
	ctx, cancel := context.WithCancel(context.Background())
	return &Worker{
		cfg:             cfg,
		deps:            deps,
		taskQueue:       queue.NewInmem(256),
		transcribeQueue: queue.NewInmem(256),
		refineQueue:     queue.NewInmem(256),
		ctx:             ctx,
		cancel:          cancel,
		cancelMap:       map[string]context.CancelFunc{},
	}
}

// Start launches the configured worker loops exactly once.
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

// Close cancels the worker context and closes the active queues.
func (w *Worker) Close() {
	if w.cancel != nil {
		w.cancel()
	}
	if w.cfg.SplitTaskQueues {
		w.transcribeQueue.Close()
		w.refineQueue.Close()
		return
	}
	w.taskQueue.Close()
}

// EnqueueTranscribe queues an audio transcription task.
func (w *Worker) EnqueueTranscribe(jobID string) {
	t := queue.Task{JobID: jobID, Kind: queue.TaskAudioTranscribe}
	if w.cfg.SplitTaskQueues {
		_ = w.transcribeQueue.Enqueue(t)
		w.setQueueLen()
		return
	}
	_ = w.taskQueue.Enqueue(t)
	w.setQueueLen()
}

// EnqueueRefine queues a transcript refine task.
func (w *Worker) EnqueueRefine(jobID string) {
	t := queue.Task{JobID: jobID, Kind: queue.TaskAudioRefine}
	if w.cfg.SplitTaskQueues {
		_ = w.refineQueue.Enqueue(t)
		w.setQueueLen()
		return
	}
	_ = w.taskQueue.Enqueue(t)
	w.setQueueLen()
}

// EnqueuePDFExtract queues a PDF extraction task.
func (w *Worker) EnqueuePDFExtract(jobID string) {
	t := queue.Task{JobID: jobID, Kind: queue.TaskPDFExtract}
	if w.cfg.SplitTaskQueues {
		_ = w.refineQueue.Enqueue(t)
		w.setQueueLen()
		return
	}
	_ = w.taskQueue.Enqueue(t)
	w.setQueueLen()
}

// RequeuePending rebuilds the worker queues from the persisted snapshot on startup.
func (w *Worker) RequeuePending(jobs map[string]*model.Job) {
	for id, job := range jobs {
		if job == nil || job.IsTrashed {
			continue
		}
		switch job.Status {
		case w.cfg.StatusPending, w.cfg.StatusRunning:
			if job.FileType == "pdf" && w.deps.BlobSvc != nil && w.deps.BlobSvc.HasPDFOriginal(id) {
				w.EnqueuePDFExtract(id)
			} else if w.deps.BlobSvc != nil && w.deps.BlobSvc.HasAudioAAC(id) {
				w.EnqueueTranscribe(id)
			}
		case w.cfg.StatusRefiningPending, w.cfg.StatusRefining:
			if w.deps.BlobSvc != nil && w.deps.BlobSvc.HasTranscript(id) {
				w.EnqueueRefine(id)
			}
		}
	}
}

// Cancel cancels the currently running job task if it has an active cancel func.
func (w *Worker) Cancel(jobID string) {
	w.cancelMu.Lock()
	cancel := w.cancelMap[jobID]
	w.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// setCancel stores or removes the cancel func for the active job.
func (w *Worker) setCancel(jobID string, cancel context.CancelFunc) {
	w.cancelMu.Lock()
	defer w.cancelMu.Unlock()
	if cancel == nil {
		delete(w.cancelMap, jobID)
		return
	}
	w.cancelMap[jobID] = cancel
}

// setQueueLen updates the observable queue length metric.
func (w *Worker) setQueueLen() {
	if w.deps.SetQueueLength == nil {
		return
	}
	if w.cfg.SplitTaskQueues {
		w.deps.SetQueueLength(float64(w.transcribeQueue.Len() + w.refineQueue.Len()))
		return
	}
	w.deps.SetQueueLength(float64(w.taskQueue.Len()))
}

// workerLoop runs the single shared queue mode.
func (w *Worker) workerLoop() {
	for {
		t, err := w.taskQueue.Dequeue(w.ctx)
		if err != nil {
			return
		}
		w.deps.Logf("[WORKER] dequeued mode=single job_id=%s kind=%s", t.JobID, t.Kind)
		w.deps.IncInProgress()
		w.setQueueLen()
		w.processTask(t, false)
		w.deps.DecInProgress()
		w.setQueueLen()
	}
}

// transcribeWorkerLoop runs the transcribe queue in split-queue mode.
func (w *Worker) transcribeWorkerLoop() {
	for {
		t, err := w.transcribeQueue.Dequeue(w.ctx)
		if err != nil {
			return
		}
		w.deps.Logf("[WORKER] dequeued mode=transcribe job_id=%s kind=%s", t.JobID, t.Kind)
		w.deps.IncInProgress()
		w.setQueueLen()
		w.processTask(t, true)
		w.deps.DecInProgress()
		w.setQueueLen()
	}
}

// refineWorkerLoop runs the refine/pdf queue in split-queue mode.
func (w *Worker) refineWorkerLoop() {
	for {
		t, err := w.refineQueue.Dequeue(w.ctx)
		if err != nil {
			return
		}
		w.deps.Logf("[WORKER] dequeued mode=refine job_id=%s kind=%s", t.JobID, t.Kind)
		w.deps.IncInProgress()
		w.setQueueLen()
		w.processTask(t, true)
		w.deps.DecInProgress()
		w.setQueueLen()
	}
}

func (w *Worker) processTask(t queue.Task, splitMode bool) {
	job := w.deps.GetJob(t.JobID)
	if job == nil || job.IsTrashed {
		return
	}

	switch t.Kind {
	case queue.TaskAudioTranscribe:
		if job.Status != w.cfg.StatusPending && job.Status != w.cfg.StatusRunning {
			return
		}
		if job.FileType == "pdf" {
			if err := w.taskExtractPDF(t.JobID); err != nil {
				w.deps.Errf("worker.extractPDF", err, "job_id=%s", t.JobID)
			}
			return
		}
		if err := w.taskTranscribe(t.JobID); err != nil {
			w.deps.Errf("worker.transcribe", err, "job_id=%s", t.JobID)
			return
		}
		updated := w.deps.GetJob(t.JobID)
		if updated == nil || updated.Status != w.cfg.StatusRefiningPending {
			return
		}
		if splitMode {
			w.EnqueueRefine(t.JobID)
			w.deps.Logf("[WORKER] queued refine job_id=%s", t.JobID)
			return
		}
		w.finalizeRefine(t.JobID)
	case queue.TaskAudioRefine:
		if job.Status != w.cfg.StatusRefiningPending && job.Status != w.cfg.StatusRefining {
			return
		}
		w.finalizeRefine(t.JobID)
	case queue.TaskPDFExtract:
		if job.Status != w.cfg.StatusPending && job.Status != w.cfg.StatusRunning {
			return
		}
		if err := w.taskExtractPDF(t.JobID); err != nil {
			w.deps.Errf("worker.extractPDF", err, "job_id=%s", t.JobID)
		}
	}
}

func (w *Worker) finalizeRefine(jobID string) {
	job := w.deps.GetJob(jobID)
	if job == nil || job.IsTrashed {
		return
	}
	if w.deps.BlobSvc == nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		if w.deps.Errf != nil {
			w.deps.Errf("worker.blobSvc", errors.New("missing blob service"), "job_id=%s", jobID)
		}
		return
	}
	b, err := w.deps.BlobSvc.LoadTranscript(jobID)
	if err != nil {
		w.deps.Errf("worker.loadTranscriptBlob", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		return
	}
	if err := w.taskRefining(jobID, string(b)); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.Errf("worker.refine", err, "job_id=%s", jobID)
		return
	}
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return
	}
	w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusCompleted, "result": "db://transcript"})
	w.deps.Logf("[WORKER] completed job_id=%s result=db://transcript", jobID)
}

func (w *Worker) taskTranscribe(jobID string) error {
	w.deps.Logf("[TRANSCRIBE] start job_id=%s input=db://wav", jobID)
	started := time.Now()
	w.deps.SetJobFields(jobID, map[string]any{
		"status":       w.cfg.StatusRunning,
		"started_at":   started.Format("2006-01-02 15:04:05"),
		"started_ts":   float64(started.Unix()),
		"preview_text": "",
	})
	if w.deps.BlobSvc == nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return errors.New("missing blob service")
	}
	if w.deps.WhisperRunner == nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return errors.New("missing whisper runner")
	}
	w.deps.BlobSvc.DeletePreview(jobID)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(w.cfg.JobTimeoutSec)*time.Second)
	w.setCancel(jobID, cancel)
	defer func() {
		cancel()
		w.setCancel(jobID, nil)
	}()

	job := w.deps.GetJob(jobID)
	totalSec := job.MediaDurationSeconds
	audioBytes, err := w.deps.BlobSvc.LoadAudioAAC(jobID)
	if err != nil {
		w.deps.Errf("transcribe.loadAudioBlob", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return err
	}
	aacPath := filepath.Join(w.cfg.TmpFolder, jobID+".m4a")
	wavPath := filepath.Join(w.cfg.TmpFolder, jobID+".wav")
	if err := os.WriteFile(aacPath, audioBytes, 0o644); err != nil {
		w.deps.Errf("transcribe.writeTempAac", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if err := w.deps.ConvertToWav(aacPath, wavPath); err != nil {
		_ = os.Remove(aacPath)
		w.deps.Errf("transcribe.convertToWav", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return err
	}
	_ = os.Remove(aacPath)
	wavBytes, err := os.ReadFile(wavPath)
	if err != nil {
		_ = os.Remove(wavPath)
		w.deps.Errf("transcribe.readTempWav", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return err
	}
	runResult, err := w.deps.WhisperRunner.RunFromBlob(ctx, jobID, wavBytes, totalSec)
	if err != nil {
		statusLabel := "failure"
		fields := map[string]any{"status": w.cfg.StatusFailed}
		if errors.Is(err, context.DeadlineExceeded) {
			fields["status_detail"] = "타임아웃"
			statusLabel = "timeout"
		}
		w.deps.SetJobFields(jobID, fields)
		w.deps.IncJobsTotal(statusLabel)
		w.deps.Errf("transcribe.runWhisper", err, "job_id=%s", jobID)
		_ = os.Remove(wavPath)
		return err
	}
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return nil
	}

	if err := w.deps.BlobSvc.SaveTranscript(jobID, []byte(runResult.TimelineText)); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		w.deps.Errf("transcribe.saveTranscriptBlob", err, "job_id=%s", jobID)
		return err
	}
	if len(runResult.TranscriptJSON) > 0 {
		if err := w.deps.BlobSvc.SaveTranscriptJSON(jobID, runResult.TranscriptJSON); err != nil {
			w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
			w.deps.IncJobsTotal("failure")
			w.deps.Errf("transcribe.saveTranscriptJSONBlob", err, "job_id=%s", jobID)
			return err
		}
	}
	w.deps.BlobSvc.DeletePreview(jobID)

	completed := time.Now()
	w.deps.IncJobsTotal("success")
	w.deps.ObserveJobDuration(completed.Sub(started).Seconds())

	nextStatus := w.cfg.StatusCompleted
	if job.RefineEnabled && w.deps.HasGeminiConfigured() {
		nextStatus = w.cfg.StatusRefiningPending
	}

	w.deps.SetJobFields(jobID, map[string]any{
		"status":       nextStatus,
		"result":       "db://transcript",
		"preview_text": "",
		"completed_at": completed.Format("2006-01-02 15:04:05"),
		"completed_ts": float64(completed.Unix()),
		"duration":     intutil.FormatSeconds(int(completed.Sub(started).Seconds())),
	})
	_ = os.Remove(wavPath)
	w.deps.Logf("[TRANSCRIBE] cleaned input file job_id=%s", jobID)
	w.deps.Logf("[TRANSCRIBE] done job_id=%s output=db://transcript status=%s duration_sec=%d", jobID, nextStatus, int(completed.Sub(started).Seconds()))
	return nil
}

func (w *Worker) taskRefining(jobID, timelineText string) error {
	w.deps.Logf("[REFINE] start job_id=%s", jobID)
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return nil
	}
	w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusRefining})
	if !w.deps.HasGeminiConfigured() {
		w.deps.Logf("[REFINE] skipped job_id=%s reason=no gemini key", jobID)
		return nil
	}
	job := w.deps.GetJob(jobID)
	desc := w.buildRefineDescription(job)
	refined, err := w.deps.RefineTranscript(timelineText, desc)
	if err != nil || strings.TrimSpace(refined) == "" {
		if err != nil {
			w.deps.Errf("refine.refineTranscript", err, "job_id=%s", jobID)
		} else {
			w.deps.Logf("[REFINE] empty result job_id=%s", jobID)
		}
		return err
	}
	if w.deps.BlobSvc == nil {
		return errors.New("missing blob service")
	}
	if err := w.deps.BlobSvc.SaveRefined(jobID, []byte(refined)); err != nil {
		w.deps.Errf("refine.saveRefinedBlob", err, "job_id=%s", jobID)
		return err
	}
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return nil
	}
	w.deps.SetJobFields(jobID, map[string]any{"result_refined": "db://refined"})
	w.deps.Logf("[REFINE] done job_id=%s output=db://refined", jobID)
	return nil
}

func (w *Worker) buildRefineDescription(job *model.Job) string {
	base := strings.TrimSpace(job.Description)
	ownerID := strings.TrimSpace(job.OwnerID)
	tags := w.deps.UniqueStrings(job.Tags)
	if ownerID == "" || len(tags) == 0 {
		return base
	}

	descMap, err := w.deps.GetTagDescriptions(ownerID, tags)
	if err != nil {
		w.deps.Errf("refine.getTagDescriptions", err, "owner_id=%s", ownerID)
		return base
	}

	tagLines := make([]string, 0, len(tags))
	for _, t := range tags {
		d := strings.TrimSpace(descMap[t])
		if d == "" {
			continue
		}
		tagLines = append(tagLines, fmt.Sprintf("[%s] %s", t, d))
	}
	if len(tagLines) == 0 {
		return base
	}
	if base == "" {
		return strings.Join(tagLines, "\n")
	}
	return base + "\n\n" + strings.Join(tagLines, "\n")
}
