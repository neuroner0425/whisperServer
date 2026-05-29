// worker.go contains the shared queue loops and audio/refine worker orchestration.
package worker

import (
	"context"
	"encoding/json"
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

// Config defines worker runtime settings and status labels.
type Config struct {
	SplitTaskQueues             bool
	RequeueVADFallbackOnStartup bool
	TmpFolder                   string
	ModelDir                    string
	WhisperCLI                  string
	JobTimeoutSec               int
	PDFBatchTimeoutSec          int
	PDFMaxPages                 int
	PDFMaxPagesPerRequest       int
	PDFMaxRenderedImageBytes    int64
	DevMode                     bool
	ProgressRe                  *regexp.Regexp
	StatusPending               string
	StatusRunning               string
	StatusRefiningPending       string
	StatusRefining              string
	StatusCompleted             string
	StatusFailed                string
}

// DocumentPageImage is one rendered PDF page passed to Gemini extraction.
type DocumentPageImage struct {
	PageIndex int
	MIMEType  string
	Data      []byte
}

// DocumentChunk is one batch of rendered PDF pages sent to Gemini.
type DocumentChunk struct {
	ChunkIndex  int
	TotalChunks int
	StartPage   int
	EndPage     int
	TotalPages  int
	Images      []DocumentPageImage
}

// Deps provides runtime, blob, integration, and metric hooks used by the worker.
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
	HasGeminiConfigured           func() bool
	PolishTranscriptTimeline      func(string, string) (string, error)
	StructureTranscriptParagraphs func(string, string) (string, error)
	CountPDFPages                 func(string) (int, error)
	RenderPDFToJPEGs              func(string, string) ([]string, error)
	ExtractDocumentChunk          func(context.Context, DocumentChunk, string) ([]byte, error)
	BuildConsistencyContext       func([]byte) (string, error)
	MergeDocumentJSON             func(...[]byte) ([]byte, error)
	UniqueStrings                 func([]string) []string
	GetTagDescriptions            func(string, []string) (map[string]string, error)
	Logf                          func(string, ...any)
	Errf                          func(string, error, string, ...any)
	IncInProgress                 func()
	DecInProgress                 func()
	SetQueueLength                func(float64)
	IncJobsTotal                  func(string)
	ObserveJobDuration            func(float64)
}

// Worker consumes queued tasks and executes transcription, refine, and PDF flows.
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
		if w.cfg.RequeueVADFallbackOnStartup && w.shouldRequeueVADFallback(id, job) {
			w.resetForVADFallbackRetranscribe(id, job)
			w.EnqueueTranscribe(id)
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
			if w.deps.BlobSvc != nil && w.deps.BlobSvc.HasTranscriptJSON(id) {
				w.EnqueueRefine(id)
			}
		}
	}
}

func (w *Worker) shouldRequeueVADFallback(jobID string, job *model.Job) bool {
	if job == nil || job.FileType == "pdf" || w.deps.BlobSvc == nil {
		return false
	}
	if !w.deps.BlobSvc.HasAudioAAC(jobID) || !w.deps.BlobSvc.HasTranscriptJSON(jobID) {
		return false
	}
	if !w.isVADFallbackRequeueStatus(job) {
		return false
	}
	data, err := w.deps.BlobSvc.LoadTranscriptJSON(jobID)
	if err != nil {
		w.deps.Errf("worker.vadFallback.loadTranscriptJSON", err, "job_id=%s", jobID)
		return false
	}
	reason, err := intwhisper.NeedsVADFallbackTranscriptJSON(data)
	if err != nil {
		w.deps.Errf("worker.vadFallback.inspectTranscriptJSON", err, "job_id=%s", jobID)
		return false
	}
	if reason == "" {
		return false
	}
	w.deps.Logf("[WORKER] queued VAD-off retranscribe job_id=%s reason=%s", jobID, reason)
	return true
}

func (w *Worker) resetForVADFallbackRetranscribe(jobID string, job *model.Job) {
	w.deps.SetJobFields(jobID, map[string]any{
		"result":           "",
		"result_refined":   "",
		"status":           w.cfg.StatusPending,
		"status_code":      model.JobStatusPendingCode,
		"phase":            "",
		"progress_percent": 0,
		"progress_label":   "",
		"preview_text":     "",
		"started_at":       "",
		"started_ts":       0,
		"completed_at":     "",
		"completed_ts":     0,
		"duration":         "",
		"status_detail":    "VAD 누락 감지로 재전사 대기",
	})
	w.deps.BlobSvc.DeletePreview(jobID)
	w.deps.BlobSvc.DeleteRefinedTimeline(jobID)
	w.deps.BlobSvc.DeleteRefined(jobID)
	if job != nil && job.RefineEnabled {
		w.deps.Logf("[WORKER] VAD fallback retranscribe will re-run refine job_id=%s", jobID)
	}
}

func (w *Worker) isVADFallbackRequeueStatus(job *model.Job) bool {
	if job == nil {
		return false
	}
	switch job.StatusCode {
	case model.JobStatusCompletedCode, model.JobStatusRefiningPendingCode, model.JobStatusRefiningCode, model.JobStatusRefineFailedCode:
		return true
	}
	switch job.Status {
	case w.cfg.StatusCompleted, w.cfg.StatusRefiningPending, w.cfg.StatusRefining, model.JobStatusName(model.JobStatusRefineFailedCode):
		return true
	default:
		return false
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
		w.deps.SetJobFields(jobID, map[string]any{
			"status":        w.cfg.StatusFailed,
			"status_code":   model.JobStatusRefineFailedCode,
			"status_detail": "정제 서비스를 사용할 수 없습니다.",
		})
		if w.deps.Errf != nil {
			w.deps.Errf("worker.blobSvc", errors.New("missing blob service"), "job_id=%s", jobID)
		}
		return
	}
	timelineText, err := w.deps.BlobSvc.LoadTranscriptTimelineText(jobID)
	if err != nil {
		w.deps.Errf("worker.loadTranscriptJSON", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{
			"status":        w.cfg.StatusFailed,
			"status_code":   model.JobStatusRefineFailedCode,
			"status_detail": "원본 전사 결과를 불러오지 못했습니다.",
		})
		return
	}
	if err := w.taskRefining(jobID, timelineText); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusRefineFailedCode})
		w.deps.Errf("worker.refine", err, "job_id=%s", jobID)
		return
	}
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return
	}
	w.deps.SetJobFields(jobID, map[string]any{
		"status":         w.cfg.StatusCompleted,
		"status_code":    model.JobStatusCompletedCode,
		"status_detail":  "",
		"phase":          "",
		"progress_label": "",
		"result":         "db://transcript_json",
	})
	w.deps.Logf("[WORKER] completed job_id=%s result=db://transcript_json", jobID)
}

func (w *Worker) taskTranscribe(jobID string) error {
	w.deps.Logf("[TRANSCRIBE] start job_id=%s input=db://wav", jobID)
	started := time.Now()
	w.deps.SetJobFields(jobID, map[string]any{
		"status":        w.cfg.StatusRunning,
		"status_code":   model.JobStatusRunningCode,
		"status_detail": "",
		"started_at":    started.Format("2006-01-02 15:04:05"),
		"started_ts":    float64(started.Unix()),
		"preview_text":  "",
	})
	if w.deps.BlobSvc == nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusTranscribeFailedCode})
		w.deps.IncJobsTotal("failure")
		return errors.New("missing blob service")
	}
	w.deps.BlobSvc.DeletePreview(jobID)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(w.cfg.JobTimeoutSec)*time.Second)
	w.setCancel(jobID, cancel)
	defer func() {
		cancel()
		w.setCancel(jobID, nil)
	}()
	job := w.deps.GetJob(jobID)
	if job == nil || job.IsTrashed {
		return nil
	}
	if w.cfg.DevMode {
		return w.taskTranscribeDev(ctx, jobID, job, started)
	}
	if w.deps.WhisperRunner == nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusTranscribeFailedCode})
		w.deps.IncJobsTotal("failure")
		return errors.New("missing whisper runner")
	}

	totalSec := job.MediaDurationSeconds
	audioBytes, err := w.deps.BlobSvc.LoadAudioAAC(jobID)
	if err != nil {
		w.deps.Errf("transcribe.loadAudioBlob", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusTranscribeFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}
	aacPath := filepath.Join(w.cfg.TmpFolder, jobID+".m4a")
	wavPath := filepath.Join(w.cfg.TmpFolder, jobID+".wav")
	if err := os.WriteFile(aacPath, audioBytes, 0o644); err != nil {
		w.deps.Errf("transcribe.writeTempAac", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusAudioConvertFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if err := w.deps.ConvertToWav(aacPath, wavPath); err != nil {
		_ = os.Remove(aacPath)
		w.deps.Errf("transcribe.convertToWav", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusAudioConvertFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}
	_ = os.Remove(aacPath)
	wavBytes, err := os.ReadFile(wavPath)
	if err != nil {
		_ = os.Remove(wavPath)
		w.deps.Errf("transcribe.readTempWav", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusAudioConvertFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}
	runResult, err := w.deps.WhisperRunner.RunFromBlob(ctx, jobID, wavBytes, totalSec)
	if err != nil {
		statusLabel := "failure"
		fields := map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusTranscribeFailedCode}
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

	if len(runResult.TranscriptJSON) > 0 {
		if err := w.deps.BlobSvc.SaveTranscriptJSON(jobID, runResult.TranscriptJSON); err != nil {
			w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusTranscribeFailedCode})
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
		"status":        nextStatus,
		"status_code":   model.JobStatusCode(nextStatus),
		"result":        "db://transcript_json",
		"status_detail": "",
		"phase":         "",
		"preview_text":  "",
		"completed_at":  completed.Format("2006-01-02 15:04:05"),
		"completed_ts":  float64(completed.Unix()),
		"duration":      intutil.FormatSeconds(int(completed.Sub(started).Seconds())),
	})
	_ = os.Remove(wavPath)
	w.deps.Logf("[TRANSCRIBE] cleaned input file job_id=%s", jobID)
	w.deps.Logf("[TRANSCRIBE] done job_id=%s output=db://transcript_json status=%s duration_sec=%d", jobID, nextStatus, int(completed.Sub(started).Seconds()))
	return nil
}

func (w *Worker) taskRefining(jobID, timelineText string) error {
	w.deps.Logf("[REFINE] start job_id=%s", jobID)
	updated := w.deps.GetJob(jobID)
	if updated == nil || updated.IsTrashed {
		return nil
	}
	previousStatusDetail := updated.StatusDetail
	w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusRefining, "status_detail": ""})
	if w.cfg.DevMode {
		return w.taskRefiningDev(jobID, timelineText)
	}
	if !w.deps.HasGeminiConfigured() {
		w.deps.Logf("[REFINE] skipped job_id=%s reason=no gemini key", jobID)
		return nil
	}
	if w.deps.BlobSvc == nil {
		return errors.New("missing blob service")
	}
	if w.deps.PolishTranscriptTimeline == nil || w.deps.StructureTranscriptParagraphs == nil {
		return errors.New("missing refine functions")
	}
	job := w.deps.GetJob(jobID)
	if job == nil || job.IsTrashed {
		return nil
	}
	desc := w.appendPreviousRefineFailureContext(jobID, w.buildRefineDescription(job), previousStatusDetail)
	w.saveRefineTextArtifact(jobID, "refine_input_timeline", timelineText)

	polishedTimeline, err := w.loadOrPolishRefinedTimeline(jobID, timelineText, desc)
	if err != nil {
		w.saveRefineDiagnostics(jobID, "polish_failure", timelineText, "", "", err)
		return err
	}
	w.saveRefineTextArtifact(jobID, "refine_polished_timeline_candidate", polishedTimeline)
	w.saveRefineDiagnostics(jobID, "polish", timelineText, polishedTimeline, "")
	w.deps.SetJobFields(jobID, map[string]any{
		"phase":            "전사 정제 중: 문단 구성",
		"progress_percent": 80,
		"progress_label":   "정제 중",
	})
	refined, err := w.deps.StructureTranscriptParagraphs(polishedTimeline, desc)
	if err != nil || strings.TrimSpace(refined) == "" {
		if err != nil {
			w.deps.SetJobFields(jobID, map[string]any{"status_detail": "전사 정제 중 문단 구성에 실패했습니다."})
			w.deps.Errf("refine.structureTranscriptParagraphs", err, "job_id=%s", jobID)
		} else {
			w.deps.SetJobFields(jobID, map[string]any{"status_detail": "전사 정제 중 문단 구성 결과가 비어 있습니다."})
			w.deps.Logf("[REFINE] empty result job_id=%s", jobID)
			err = errors.New("empty refined result")
		}
		w.saveRefineDiagnostics(jobID, "structure_failure", polishedTimeline, "", "", err)
		return err
	}
	w.saveRefineTextArtifact(jobID, "refine_structured_candidate", refined)
	w.saveRefineDiagnostics(jobID, "structure", polishedTimeline, "", refined)
	if coverageErr := validateRefinedCoverage(timelineText, refined); coverageErr != nil {
		w.saveRefineDiagnostics(jobID, "coverage_failure", timelineText, "", refined, coverageErr)
		w.deps.SetJobFields(jobID, map[string]any{"status_detail": coverageErr.Error()})
		return coverageErr
	}
	if err := w.deps.BlobSvc.SaveRefined(jobID, []byte(refined)); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status_detail": "최종 정제 결과를 저장하지 못했습니다."})
		w.deps.Errf("refine.saveRefinedBlob", err, "job_id=%s", jobID)
		return err
	}
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return nil
	}
	w.deps.SetJobFields(jobID, map[string]any{
		"result_refined":   "db://refined",
		"progress_percent": 100,
		"phase":            "",
		"progress_label":   "",
		"status_detail":    "",
	})
	w.deps.Logf("[REFINE] done job_id=%s output=db://refined", jobID)
	return nil
}

type refineDiagnostics struct {
	Step                 string   `json:"step"`
	OriginalLineCount    int      `json:"original_line_count"`
	OriginalFirstTimes   []string `json:"original_first_times,omitempty"`
	OriginalLastTimes    []string `json:"original_last_times,omitempty"`
	PolishedLineCount    int      `json:"polished_line_count,omitempty"`
	PolishedFirstTimes   []string `json:"polished_first_times,omitempty"`
	PolishedLastTimes    []string `json:"polished_last_times,omitempty"`
	RefinedSentenceCount int      `json:"refined_sentence_count,omitempty"`
	RefinedFirstTimes    []string `json:"refined_first_times,omitempty"`
	RefinedLastTimes     []string `json:"refined_last_times,omitempty"`
	MissingFromOutput    []string `json:"missing_from_output,omitempty"`
	ExtraOutputTimes     []string `json:"extra_output_times,omitempty"`
	DuplicateOutputTimes []string `json:"duplicate_output_times,omitempty"`
	FailureCause         string   `json:"failure_cause,omitempty"`
	CreatedAt            string   `json:"created_at"`
}

func (w *Worker) saveRefineTextArtifact(jobID, kind, data string) {
	if w.deps.BlobSvc == nil || strings.TrimSpace(data) == "" {
		return
	}
	if err := w.deps.BlobSvc.SaveRefineArtifact(jobID, kind, data); err != nil && w.deps.Errf != nil {
		w.deps.Errf("refine.saveArtifact", err, "job_id=%s kind=%s", jobID, kind)
	}
}

func (w *Worker) saveRefineDiagnostics(jobID, step, originalTimeline, polishedTimeline, refinedJSON string, causes ...error) {
	if w.deps.BlobSvc == nil {
		return
	}
	diag := buildRefineDiagnostics(step, originalTimeline, polishedTimeline, refinedJSON)
	if len(causes) > 0 && causes[0] != nil {
		diag.FailureCause = causes[0].Error()
	}
	b, err := json.MarshalIndent(diag, "", "  ")
	if err != nil {
		if w.deps.Errf != nil {
			w.deps.Errf("refine.marshalDiagnostics", err, "job_id=%s step=%s", jobID, step)
		}
		return
	}
	nowKind := "refine_diagnostics_" + time.Now().Format("20060102_150405_000000000")
	for _, kind := range []string{"refine_diagnostics", nowKind} {
		if err := w.deps.BlobSvc.SaveRefineArtifact(jobID, kind, string(b)); err != nil && w.deps.Errf != nil {
			w.deps.Errf("refine.saveDiagnostics", err, "job_id=%s kind=%s", jobID, kind)
		}
	}
}

func buildRefineDiagnostics(step, originalTimeline, polishedTimeline, refinedJSON string) refineDiagnostics {
	originalTimes := extractTimelineTimestamps(originalTimeline)
	polishedTimes := extractTimelineTimestamps(polishedTimeline)
	refinedTimes, _ := extractRefinedSentenceTimestamps(refinedJSON)
	compareBase := originalTimes
	compareOutput := polishedTimes
	if len(refinedTimes) > 0 {
		compareOutput = refinedTimes
	}
	return refineDiagnostics{
		Step:                 step,
		OriginalLineCount:    len(originalTimes),
		OriginalFirstTimes:   firstN(originalTimes, 5),
		OriginalLastTimes:    lastN(originalTimes, 5),
		PolishedLineCount:    len(polishedTimes),
		PolishedFirstTimes:   firstN(polishedTimes, 5),
		PolishedLastTimes:    lastN(polishedTimes, 5),
		RefinedSentenceCount: len(refinedTimes),
		RefinedFirstTimes:    firstN(refinedTimes, 5),
		RefinedLastTimes:     lastN(refinedTimes, 5),
		MissingFromOutput:    limitStrings(missingTimes(compareBase, compareOutput), 100),
		ExtraOutputTimes:     limitStrings(missingTimes(compareOutput, compareBase), 100),
		DuplicateOutputTimes: limitStrings(duplicateTimes(compareOutput), 100),
		CreatedAt:            time.Now().Format(time.RFC3339),
	}
}

func (w *Worker) loadOrPolishRefinedTimeline(jobID, timelineText, desc string) (string, error) {
	if w.deps.BlobSvc.HasRefinedTimeline(jobID) {
		b, err := w.deps.BlobSvc.LoadRefinedTimeline(jobID)
		if err != nil {
			w.deps.SetJobFields(jobID, map[string]any{"status_detail": "정제 중간 결과를 불러오지 못했습니다."})
			return "", err
		}
		polishedTimeline := strings.TrimSpace(string(b))
		if polishedTimeline != "" {
			polishedTimeline, repaired, err := repairTimelineTimestamps(timelineText, polishedTimeline)
			if err != nil {
				w.saveRefineTextArtifact(jobID, "refine_invalid_cached_timeline", polishedTimeline)
				w.deps.BlobSvc.DeleteRefinedTimeline(jobID)
				w.deps.Logf("[REFINE] discarded ambiguous refined_timeline job_id=%s", jobID)
				polishedTimeline = ""
			}
			if repaired {
				if err := w.deps.BlobSvc.SaveRefinedTimeline(jobID, []byte(polishedTimeline)); err != nil {
					return "", err
				}
				w.deps.Logf("[REFINE] repaired cached refined_timeline timestamps job_id=%s", jobID)
			}
			if polishedTimeline != "" {
				if err := validateTimelineCoverage(timelineText, polishedTimeline); err == nil {
					w.deps.Logf("[REFINE] reuse refined_timeline job_id=%s", jobID)
					return polishedTimeline, nil
				}
			}
			if polishedTimeline != "" {
				w.saveRefineTextArtifact(jobID, "refine_invalid_cached_timeline", polishedTimeline)
				w.deps.BlobSvc.DeleteRefinedTimeline(jobID)
				w.deps.Logf("[REFINE] discarded invalid refined_timeline job_id=%s", jobID)
			}
		}
	}

	w.deps.SetJobFields(jobID, map[string]any{
		"phase":            "전사 정제 중: 문장 다듬기",
		"progress_percent": 55,
		"progress_label":   "정제 중",
	})
	polishedTimeline, err := w.deps.PolishTranscriptTimeline(timelineText, desc)
	if err != nil || strings.TrimSpace(polishedTimeline) == "" {
		if err != nil {
			w.deps.SetJobFields(jobID, map[string]any{"status_detail": "전사 정제 중 문장 다듬기에 실패했습니다."})
			w.deps.Errf("refine.polishTranscriptTimeline", err, "job_id=%s", jobID)
			return "", err
		}
		w.deps.SetJobFields(jobID, map[string]any{"status_detail": "전사 정제 중 문장 다듬기 결과가 비어 있습니다."})
		return "", errors.New("empty refined timeline")
	}
	polishedTimeline = strings.TrimSpace(polishedTimeline)
	w.saveRefineTextArtifact(jobID, "refine_polished_timeline_raw_candidate", polishedTimeline)
	var repaired bool
	polishedTimeline, repaired, err = repairTimelineTimestamps(timelineText, polishedTimeline)
	if err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status_detail": err.Error()})
		return "", err
	}
	if repaired {
		w.deps.Logf("[REFINE] restored generated timeline timestamps job_id=%s", jobID)
	}
	if err := validateTimelineCoverage(timelineText, polishedTimeline); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status_detail": err.Error()})
		return "", err
	}
	if err := w.deps.BlobSvc.SaveRefinedTimeline(jobID, []byte(polishedTimeline)); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status_detail": "정제 중간 결과를 저장하지 못했습니다."})
		return "", err
	}
	w.deps.Logf("[REFINE] saved refined_timeline job_id=%s", jobID)
	return polishedTimeline, nil
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

func (w *Worker) appendPreviousRefineFailureContext(jobID, description, statusDetail string) string {
	feedback := strings.TrimSpace(statusDetail)
	if w.deps.BlobSvc != nil {
		if diagnostics, err := w.deps.BlobSvc.LoadRefineArtifact(jobID, "refine_diagnostics"); err == nil && strings.TrimSpace(diagnostics) != "" {
			if strings.Contains(diagnostics, "_failure") || strings.Contains(diagnostics, `"failure_cause"`) {
				if len(diagnostics) > 4000 {
					diagnostics = diagnostics[:4000]
				}
				if feedback != "" {
					feedback += "\n"
				}
				feedback += strings.TrimSpace(diagnostics)
			}
		}
	}
	if feedback == "" {
		return description
	}
	retryContext := "[Previous Refinement Failure Feedback]\nThe previous attempt failed. Correct the identified omission or timestamp mismatch and do not repeat it.\n" + feedback
	if strings.TrimSpace(description) == "" {
		return retryContext
	}
	return description + "\n\n" + retryContext
}

type refinedValidationPayload struct {
	Paragraph []struct {
		Sentence []struct {
			StartTime string `json:"start_time"`
			Content   string `json:"content"`
		} `json:"sentence"`
	} `json:"paragraph"`
}

var (
	timelineLineTimestampRe    = regexp.MustCompile(`\d{2}:\d{2}:\d{2},\d{3}`)
	refinedSentenceTimestampRe = regexp.MustCompile(`^\[?\d{2}:\d{2}:\d{2},\d{3}\]?$`)
)

func validateRefinedCoverage(originalTimeline, refinedJSON string) error {
	originalTimes := extractTimelineTimestamps(originalTimeline)
	if len(originalTimes) == 0 {
		return nil
	}
	if _, err := countValidRefinedSentences(refinedJSON); err != nil {
		return err
	}
	refinedTimes, err := extractRefinedSentenceTimestamps(refinedJSON)
	if err != nil {
		return err
	}
	if err := compareTimestampSequence(originalTimes, refinedTimes); err != nil {
		return fmt.Errorf("정제 결과 timestamp가 원본과 일치하지 않습니다: %w", err)
	}
	return nil
}

func validateTimelineCoverage(originalTimeline, polishedTimeline string) error {
	originalTimes := extractTimelineTimestamps(originalTimeline)
	polishedTimes := extractTimelineTimestamps(polishedTimeline)
	if len(originalTimes) == 0 {
		return nil
	}
	if err := compareTimestampSequence(originalTimes, polishedTimes); err != nil {
		return fmt.Errorf("정제 중간 결과 timestamp가 원본과 일치하지 않습니다: %w", err)
	}
	return nil
}

func compareTimestampSequence(want, got []string) error {
	const allowedMismatchPercent = 3
	allowed := len(want) * allowedMismatchPercent / 100
	missing := missingTimes(want, got)
	extra := missingTimes(got, want)
	if len(missing) > allowed || len(extra) > allowed {
		return fmt.Errorf("원본 %d개, 결과 %d개, 누락 %d개, 추가 %d개 (허용 %d개)", len(want), len(got), len(missing), len(extra), allowed)
	}
	positions := map[string]int{}
	for i, timestamp := range want {
		positions[timestamp] = i
	}
	lastPosition := -1
	for _, timestamp := range got {
		position, ok := positions[timestamp]
		if !ok {
			continue
		}
		if position < lastPosition {
			return fmt.Errorf("timestamp 순서가 변경되었습니다: %s", timestamp)
		}
		lastPosition = position
	}
	return nil
}

func repairTimelineTimestamps(originalTimeline, polishedTimeline string) (string, bool, error) {
	originalTimes := extractTimelineTimestamps(originalTimeline)
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(polishedTimeline), "\r\n", "\n"), "\n")
	polishedTimes := extractTimelineTimestamps(polishedTimeline)
	if len(originalTimes) == 0 || len(lines) != len(polishedTimes) {
		return polishedTimeline, false, nil
	}

	replacements := make([]string, len(polishedTimes))
	copy(replacements, polishedTimes)
	if len(originalTimes) == len(polishedTimes) {
		copy(replacements, originalTimes)
	} else {
		const toleranceMilliseconds = int64(1000)
		originalPositions := make(map[string]int, len(originalTimes))
		reserved := make(map[int]bool, len(polishedTimes))
		for i, timestamp := range originalTimes {
			originalPositions[timestamp] = i
		}
		for _, timestamp := range polishedTimes {
			if position, ok := originalPositions[timestamp]; ok {
				reserved[position] = true
			}
		}
		lastPosition := -1
		used := map[int]bool{}
		for i, timestamp := range polishedTimes {
			if position, ok := originalPositions[timestamp]; ok {
				if position > lastPosition {
					lastPosition = position
					used[position] = true
				}
				continue
			}
			candidates := make([]int, 0, 2)
			for position, originalTimestamp := range originalTimes {
				if position <= lastPosition || reserved[position] || used[position] {
					continue
				}
				if timestampDistanceMilliseconds(timestamp, originalTimestamp) <= toleranceMilliseconds {
					candidates = append(candidates, position)
				}
			}
			if len(candidates) > 1 {
				return polishedTimeline, false, fmt.Errorf("정제 중간 결과 timestamp가 여러 원본 문장과 1초 이내로 일치하여 대응을 확정할 수 없습니다: %s", timestamp)
			}
			if len(candidates) == 1 {
				position := candidates[0]
				replacements[i] = originalTimes[position]
				lastPosition = position
				used[position] = true
			}
		}
	}

	changed := false
	repaired := make([]string, 0, len(lines))
	for i, line := range lines {
		match := timelineLineTimestampRe.FindStringIndex(line)
		if len(match) != 2 {
			return polishedTimeline, false, nil
		}
		if replacements[i] != polishedTimes[i] {
			changed = true
		}
		prefix := line[:match[0]]
		suffix := line[match[1]:]
		repaired = append(repaired, prefix+replacements[i]+suffix)
	}
	if !changed {
		return polishedTimeline, false, nil
	}
	return strings.Join(repaired, "\n"), true, nil
}

func timestampDistanceMilliseconds(a, b string) int64 {
	parse := func(value string) int64 {
		var hours, minutes, seconds, milliseconds int64
		_, _ = fmt.Sscanf(normalizeTimestamp(value), "%d:%d:%d,%d", &hours, &minutes, &seconds, &milliseconds)
		return (((hours*60)+minutes)*60+seconds)*1000 + milliseconds
	}
	diff := parse(a) - parse(b)
	if diff < 0 {
		return -diff
	}
	return diff
}

func countTimestampedTimelineLines(timeline string) int {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(timeline, "\r\n", "\n"), "\n") {
		if timelineLineTimestampRe.MatchString(line) && strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func countValidRefinedSentences(refinedJSON string) (int, error) {
	var payload refinedValidationPayload
	if err := json.Unmarshal([]byte(refinedJSON), &payload); err != nil {
		return 0, fmt.Errorf("정제 결과 JSON을 해석하지 못했습니다: %w", err)
	}
	count := 0
	for i, paragraph := range payload.Paragraph {
		for j, sentence := range paragraph.Sentence {
			startTime := strings.TrimSpace(sentence.StartTime)
			if !refinedSentenceTimestampRe.MatchString(startTime) {
				return 0, fmt.Errorf("정제 결과 timestamp가 비어 있거나 올바르지 않습니다. paragraph=%d sentence=%d", i+1, j+1)
			}
			count++
		}
	}
	return count, nil
}

func extractTimelineTimestamps(timeline string) []string {
	out := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(timeline, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := timelineLineTimestampRe.FindString(line)
		if m == "" {
			continue
		}
		out = append(out, normalizeTimestamp(m))
	}
	return out
}

func extractRefinedSentenceTimestamps(refinedJSON string) ([]string, error) {
	if strings.TrimSpace(refinedJSON) == "" {
		return nil, nil
	}
	var payload refinedValidationPayload
	if err := json.Unmarshal([]byte(refinedJSON), &payload); err != nil {
		return nil, err
	}
	out := []string{}
	for _, paragraph := range payload.Paragraph {
		for _, sentence := range paragraph.Sentence {
			startTime := normalizeTimestamp(sentence.StartTime)
			if startTime == "" {
				continue
			}
			out = append(out, startTime)
		}
	}
	return out, nil
}

func normalizeTimestamp(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if !timelineLineTimestampRe.MatchString(value) {
		return ""
	}
	return value
}

func firstN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}

func lastN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[len(values)-n:]
}

func missingTimes(want, got []string) []string {
	counts := map[string]int{}
	for _, value := range got {
		counts[value]++
	}
	missing := []string{}
	for _, value := range want {
		if counts[value] > 0 {
			counts[value]--
			continue
		}
		missing = append(missing, value)
	}
	return missing
}

func duplicateTimes(values []string) []string {
	seen := map[string]int{}
	dupes := []string{}
	for _, value := range values {
		seen[value]++
		if seen[value] == 2 {
			dupes = append(dupes, value)
		}
	}
	return dupes
}

func limitStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func (w *Worker) taskTranscribeDev(ctx context.Context, jobID string, job *model.Job, started time.Time) error {
	w.deps.Logf("[TRANSCRIBE] dev stub start job_id=%s", jobID)
	w.deps.SetJobFields(jobID, map[string]any{
		"phase":            "DEV 전사 테스트 중",
		"progress_percent": 5,
		"progress_label":   "DEV",
	})
	if err := w.sleepWithProgress(ctx, 20*time.Second, func(percent int) {
		w.deps.SetJobFields(jobID, map[string]any{
			"phase":            "DEV 전사 테스트 중",
			"progress_percent": percent,
			"progress_label":   "DEV",
		})
	}); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusTranscribeFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return nil
	}
	payload := map[string]any{
		"segments": []map[string]string{
			{
				"from": "00:00:00,000",
				"to":   "00:00:08,000",
				"text": "DEV 모드에서 생성한 전사 테스트 결과입니다.",
			},
			{
				"from": "00:00:08,000",
				"to":   "00:00:20,000",
				"text": "실제 Whisper 전사는 실행하지 않았으며 업로드와 작업 흐름 확인용 문장입니다.",
			},
		},
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := w.deps.BlobSvc.SaveTranscriptJSON(jobID, b); err != nil {
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed, "status_code": model.JobStatusTranscribeFailedCode})
		w.deps.IncJobsTotal("failure")
		return err
	}
	w.deps.BlobSvc.DeletePreview(jobID)

	completed := time.Now()
	w.deps.IncJobsTotal("success")
	w.deps.ObserveJobDuration(completed.Sub(started).Seconds())

	nextStatus := w.cfg.StatusCompleted
	if job.RefineEnabled {
		nextStatus = w.cfg.StatusRefiningPending
	}
	w.deps.SetJobFields(jobID, map[string]any{
		"status":           nextStatus,
		"status_code":      model.JobStatusCode(nextStatus),
		"result":           "db://transcript_json",
		"phase":            "",
		"preview_text":     "",
		"completed_at":     completed.Format("2006-01-02 15:04:05"),
		"completed_ts":     float64(completed.Unix()),
		"duration":         intutil.FormatSeconds(int(completed.Sub(started).Seconds())),
		"progress_percent": 100,
		"progress_label":   "",
	})
	w.deps.Logf("[TRANSCRIBE] dev stub done job_id=%s status=%s", jobID, nextStatus)
	return nil
}

func (w *Worker) taskRefiningDev(jobID, timelineText string) error {
	job := w.deps.GetJob(jobID)
	if job == nil || job.IsTrashed {
		return nil
	}
	if w.deps.BlobSvc == nil {
		return errors.New("missing blob service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(w.cfg.JobTimeoutSec)*time.Second)
	w.setCancel(jobID, cancel)
	defer func() {
		cancel()
		w.setCancel(jobID, nil)
	}()
	desc := strings.TrimSpace(w.buildRefineDescription(job))
	if desc == "" {
		desc = "입력된 설명이 없습니다."
	}
	if strings.TrimSpace(timelineText) == "" {
		timelineText = "전사 원문이 비어 있습니다."
	}

	polishedTimeline := ""
	if w.deps.BlobSvc.HasRefinedTimeline(jobID) {
		b, err := w.deps.BlobSvc.LoadRefinedTimeline(jobID)
		if err != nil {
			w.deps.SetJobFields(jobID, map[string]any{"status_detail": "정제 중간 결과를 불러오지 못했습니다."})
			return err
		}
		polishedTimeline = strings.TrimSpace(string(b))
	}
	if polishedTimeline == "" {
		w.deps.SetJobFields(jobID, map[string]any{
			"phase":            "전사 정제 중: 문장 다듬기",
			"progress_percent": 50,
			"progress_label":   "DEV",
		})
		if err := w.sleepWithProgress(ctx, 5*time.Second, func(percent int) {
			w.deps.SetJobFields(jobID, map[string]any{
				"phase":            "전사 정제 중: 문장 다듬기",
				"progress_percent": 10 + percent*45/100,
				"progress_label":   "DEV",
			})
		}); err != nil {
			return err
		}
		polishedTimeline = strings.Join([]string{
			"[00:00:00,000] DEV 모드에서 다듬은 전사 결과입니다. 사용자 설명: " + desc,
			"[00:00:10,000] 원본 전사 예시: " + timelineText,
		}, "\n")
		if err := w.deps.BlobSvc.SaveRefinedTimeline(jobID, []byte(polishedTimeline)); err != nil {
			w.deps.SetJobFields(jobID, map[string]any{"status_detail": "정제 중간 결과를 저장하지 못했습니다."})
			return err
		}
	}

	w.deps.SetJobFields(jobID, map[string]any{
		"phase":            "전사 정제 중: 문단 구성",
		"progress_percent": 80,
		"progress_label":   "DEV",
	})
	if err := w.sleepWithProgress(ctx, 5*time.Second, func(percent int) {
		w.deps.SetJobFields(jobID, map[string]any{
			"phase":            "전사 정제 중: 문단 구성",
			"progress_percent": 55 + percent*40/100,
			"progress_label":   "DEV",
		})
	}); err != nil {
		return err
	}
	payload := map[string]any{
		"paragraph": []map[string]any{
			{
				"paragraph_summary": "DEV 정제 테스트 요약",
				"sentence": []map[string]string{
					{
						"start_time": "[00:00:00,000]",
						"content":    "DEV 모드에서 생성한 정제 결과입니다. 사용자 설명: " + desc,
					},
					{
						"start_time": "[00:00:10,000]",
						"content":    "문장 다듬기 결과 예시: " + polishedTimeline,
					},
				},
			},
		},
	}
	refined, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := w.deps.BlobSvc.SaveRefined(jobID, refined); err != nil {
		return err
	}
	if updated := w.deps.GetJob(jobID); updated == nil || updated.IsTrashed {
		return nil
	}
	w.deps.SetJobFields(jobID, map[string]any{"result_refined": "db://refined", "progress_percent": 100, "phase": "", "progress_label": "", "status_detail": ""})
	w.deps.Logf("[REFINE] dev stub done job_id=%s", jobID)
	return nil
}

func (w *Worker) sleepWithProgress(ctx context.Context, duration time.Duration, update func(int)) error {
	if duration <= 0 {
		return nil
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if update != nil {
				update(95)
			}
			return nil
		case <-ticker.C:
			if update != nil {
				elapsed := time.Since(start)
				percent := 10 + int(float64(elapsed)/float64(duration)*80)
				if percent > 95 {
					percent = 95
				}
				update(percent)
			}
		}
	}
}
