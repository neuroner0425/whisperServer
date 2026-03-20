package worker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"whisperserver/src/internal/model"
	"whisperserver/src/internal/store"
	intutil "whisperserver/src/internal/util"
)

type Config struct {
	SplitTaskQueues       bool
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
			if store.HasJobBlob(id, store.BlobKindWav) {
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
	case taskTypeTranscribe:
		if job.Status != w.cfg.StatusPending && job.Status != w.cfg.StatusRunning {
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
	case taskTypeRefine:
		if job.Status != w.cfg.StatusRefiningPending && job.Status != w.cfg.StatusRefining {
			return
		}
		w.finalizeRefine(t.jobID)
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
	wavBytes, err := store.LoadJobBlob(jobID, store.BlobKindWav)
	if err != nil {
		w.deps.Errf("transcribe.loadWavBlob", err, "job_id=%s", jobID)
		w.deps.SetJobFields(jobID, map[string]any{"status": w.cfg.StatusFailed})
		w.deps.IncJobsTotal("failure")
		return err
	}
	timelineText, err := w.runWhisperFromBlob(ctx, jobID, wavBytes, totalSec)
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
		"completed_at": completed.Format("2006-01-02 15:04:05"),
		"completed_ts": float64(completed.Unix()),
		"duration":     intutil.FormatSeconds(int(completed.Sub(started).Seconds())),
	})
	store.DeleteJobBlob(jobID, store.BlobKindWav)
	w.deps.Logf("[TRANSCRIBE] cleaned input blob job_id=%s kind=%s", jobID, store.BlobKindWav)
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

func (w *Worker) runWhisperFromBlob(ctx context.Context, jobID string, wavBytes []byte, totalSec *int) (string, error) {
	tmpDir, err := os.MkdirTemp("", "whisper-job-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	wavPath := filepath.Join(tmpDir, jobID+".wav")
	if err := os.WriteFile(wavPath, wavBytes, 0o644); err != nil {
		return "", err
	}
	return w.runWhisper(ctx, jobID, wavPath, totalSec)
}

func (w *Worker) runWhisper(ctx context.Context, jobID, wavPath string, totalSec *int) (string, error) {
	w.deps.Logf("[WHISPER] start job_id=%s wav=%s total_sec=%v", jobID, wavPath, totalSec)
	outputPath := wavPath + ".txt"
	modelBin := filepath.Join(w.cfg.ModelDir, "ggml-large-v3.bin")
	vadModel := filepath.Join(w.cfg.ModelDir, "ggml-silero-v6.2.0.bin")

	cmd := exec.CommandContext(ctx, w.cfg.WhisperCLI,
		"-m", modelBin,
		"-l", "ko",
		"--max-context", "0",
		"--no-speech-thold", "0.01",
		"--suppress-nst",
		"--vad",
		"--vad-model", vadModel,
		"--vad-threshold", "0.01",
		"--output-txt", wavPath,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		w.deps.Errf("whisper.stdoutPipe", err, "job_id=%s", jobID)
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		w.deps.Errf("whisper.stderrPipe", err, "job_id=%s", jobID)
		return "", err
	}
	if err := cmd.Start(); err != nil {
		w.deps.Errf("whisper.start", err, "job_id=%s", jobID)
		return "", err
	}

	w.deps.SetJobFields(jobID, map[string]any{"phase": "전처리 중", "progress_percent": 0, "progress_label": "전처리 중..."})

	lines := make(chan string, 256)
	var wg sync.WaitGroup
	wg.Add(2)
	go scanPipe(&wg, stdout, lines)
	go scanPipe(&wg, stderr, lines)
	go func() {
		wg.Wait()
		close(lines)
	}()

	lastPercent := -1
	maxPercent := -1
	lastProgressLog := -5
	lastPreviewLine := ""
	sawTimeline := false

	for line := range lines {
		if !strings.Contains(line, "-->") {
			continue
		}
		m := w.cfg.ProgressRe.FindStringSubmatch(line)
		if len(m) != 4 {
			continue
		}
		h, _ := strconv.ParseFloat(m[1], 64)
		mm, _ := strconv.ParseFloat(m[2], 64)
		ss, _ := strconv.ParseFloat(m[3], 64)
		startSec := h*3600 + mm*60 + ss
		percent := 0
		if totalSec != nil && *totalSec > 0 {
			percent = int((startSec / float64(*totalSec)) * 100)
		}
		if percent < maxPercent {
			percent = maxPercent
		}
		if percent == lastPercent {
			continue
		}
		w.deps.SetJobFields(jobID, map[string]any{
			"phase":            "전사 중",
			"progress_percent": percent,
			"progress_label":   fmt.Sprintf("전사 중... %d%%", percent),
		})
		lastPercent = percent
		if percent > maxPercent {
			maxPercent = percent
		}
		if line != lastPreviewLine {
			w.deps.AppendJobPreviewLine(jobID, line)
			lastPreviewLine = line
		}
		if percent >= lastProgressLog+5 || percent == 100 {
			w.deps.Logf("[WHISPER] progress job_id=%s percent=%d", jobID, percent)
			lastProgressLog = percent
		}
		sawTimeline = true
	}

	if err := cmd.Wait(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			w.deps.Errf("whisper.wait", context.DeadlineExceeded, "job_id=%s timeout_sec=%d", jobID, w.cfg.JobTimeoutSec)
			return "", context.DeadlineExceeded
		}
		w.deps.Errf("whisper.wait", err, "job_id=%s", jobID)
		return "", err
	}
	if sawTimeline {
		w.deps.SetJobFields(jobID, map[string]any{"phase": "전사 완료", "progress_percent": 100, "progress_label": "전사 완료"})
	}

	b, err := os.ReadFile(outputPath)
	if err != nil {
		w.deps.Errf("whisper.readOutput", err, "job_id=%s path=%s", jobID, outputPath)
		return "", err
	}
	_ = os.Remove(outputPath)
	w.deps.Logf("[WHISPER] done job_id=%s", jobID)
	return string(b), nil
}

func scanPipe(wg *sync.WaitGroup, r io.Reader, out chan<- string) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Split(splitOnCRLF)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			out <- line
		}
	}
}

func splitOnCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
