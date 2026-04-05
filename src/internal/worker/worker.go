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

	"whisperserver/src/internal/model"
	"whisperserver/src/internal/store"
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
	GetJob                  func(string) *model.Job
	SetJobFields            func(string, map[string]any)
	AppendJobPreviewLine    func(string, string)
	ReplaceJobPreviewText   func(string, string)
	ConvertToWav            func(string, string) error
	HasGeminiConfigured     func() bool
	RefineTranscript        func(string, string) (string, error)
	CountPDFPages           func(string) (int, error)
	RenderPDFToJPEGs        func(string, string) ([]string, error)
	ExtractDocumentChunk    func(context.Context, DocumentChunk, string) ([]byte, error)
	BuildConsistencyContext func([]byte) (string, error)
	MergeDocumentJSON       func(...[]byte) ([]byte, error)
	RenderDocumentMarkdown  func([]byte) (string, error)
	ListJobBlobKinds        func(string) ([]string, error)
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

type taskType string

const (
	taskTypeAudioTranscribe taskType = "audio_transcribe"
	taskTypeAudioRefine     taskType = "audio_refine"
	taskTypePDFExtract      taskType = "pdf_extract"
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
	t := task{jobID: jobID, kind: taskTypeAudioTranscribe}
	if w.cfg.SplitTaskQueues {
		w.transcribeQueue <- t
		w.setQueueLen()
		return
	}
	w.taskQueue <- t
	w.setQueueLen()
}

func (w *Worker) EnqueueRefine(jobID string) {
	t := task{jobID: jobID, kind: taskTypeAudioRefine}
	if w.cfg.SplitTaskQueues {
		w.refineQueue <- t
		w.setQueueLen()
		return
	}
	w.taskQueue <- t
	w.setQueueLen()
}

func (w *Worker) EnqueuePDFExtract(jobID string) {
	t := task{jobID: jobID, kind: taskTypePDFExtract}
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
			if job.FileType == "pdf" && store.HasJobBlob(id, store.BlobKindPDFOriginal) {
				w.EnqueuePDFExtract(id)
			} else if store.HasJobBlob(id, store.BlobKindAudioAAC) {
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

func (w *Worker) processTask(t task, splitMode bool) {
	job := w.deps.GetJob(t.jobID)
	if job == nil || job.IsTrashed {
		return
	}

	switch t.kind {
	case taskTypeAudioTranscribe:
		if job.Status != w.cfg.StatusPending && job.Status != w.cfg.StatusRunning {
			return
		}
		if job.FileType == "pdf" {
			if err := w.taskExtractPDF(t.jobID); err != nil {
				w.deps.Errf("worker.extractPDF", err, "job_id=%s", t.jobID)
			}
			return
		}
		if err := w.taskTranscribe(t.jobID); err != nil {
			w.deps.Errf("worker.transcribe", err, "job_id=%s", t.jobID)
			return
		}
		updated := w.deps.GetJob(t.jobID)
		if updated == nil || updated.Status != w.cfg.StatusRefiningPending {
			return
		}
		if splitMode {
			w.EnqueueRefine(t.jobID)
			w.deps.Logf("[WORKER] queued refine job_id=%s", t.jobID)
			return
		}
		w.finalizeRefine(t.jobID)
	case taskTypeAudioRefine:
		if job.Status != w.cfg.StatusRefiningPending && job.Status != w.cfg.StatusRefining {
			return
		}
		w.finalizeRefine(t.jobID)
	case taskTypePDFExtract:
		if job.Status != w.cfg.StatusPending && job.Status != w.cfg.StatusRunning {
			return
		}
		if err := w.taskExtractPDF(t.jobID); err != nil {
			w.deps.Errf("worker.extractPDF", err, "job_id=%s", t.jobID)
		}
	}
}

func (w *Worker) finalizeRefine(jobID string) {
	job := w.deps.GetJob(jobID)
	if job == nil || job.IsTrashed {
		return
	}
	b, err := store.LoadJobBlob(jobID, store.BlobKindTranscript)
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
	store.DeleteJobBlob(jobID, store.BlobKindPreview)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(w.cfg.JobTimeoutSec)*time.Second)
	w.setCancel(jobID, cancel)
	defer func() {
		cancel()
		w.setCancel(jobID, nil)
	}()

	job := w.deps.GetJob(jobID)
	totalSec := job.MediaDurationSeconds
	audioBytes, err := store.LoadJobBlob(jobID, store.BlobKindAudioAAC)
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
	timelineText, transcriptJSON, err := w.runWhisperFromBlob(ctx, jobID, wavBytes, totalSec)
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

	if err := store.SaveJobBlob(jobID, store.BlobKindTranscript, []byte(timelineText)); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		w.deps.Errf("transcribe.saveTranscriptBlob", err, "job_id=%s", jobID)
		return err
	}
	if len(transcriptJSON) > 0 {
		if err := store.SaveJobBlob(jobID, store.BlobKindTranscriptJSON, transcriptJSON); err != nil {
			w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
			w.deps.IncJobsTotal("failure")
			w.deps.Errf("transcribe.saveTranscriptJSONBlob", err, "job_id=%s", jobID)
			return err
		}
	}
	store.DeleteJobBlob(jobID, store.BlobKindPreview)

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
	if err := store.SaveJobBlob(jobID, store.BlobKindRefined, []byte(refined)); err != nil {
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
