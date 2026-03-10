package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func startWorker() {
	workerOnce.Do(func() {
		if splitTaskQueues {
			procLogf("[WORKER] start mode=split")
			go transcribeWorkerLoop()
			go refineWorkerLoop()
		} else {
			procLogf("[WORKER] start mode=single")
			go workerLoop()
		}
	})
}

func workerLoop() {
	for t := range taskQueue {
		procLogf("[WORKER] dequeued mode=single job_id=%s kind=%s", t.jobID, t.kind)
		jobsInProgress.Inc()
		setQueueLen()
		processTask(t, false)
		jobsInProgress.Dec()
		setQueueLen()
	}
}

func transcribeWorkerLoop() {
	for t := range transcribeQueue {
		procLogf("[WORKER] dequeued mode=transcribe job_id=%s kind=%s", t.jobID, t.kind)
		jobsInProgress.Inc()
		setQueueLen()
		processTask(t, true)
		jobsInProgress.Dec()
		setQueueLen()
	}
}

func refineWorkerLoop() {
	for t := range refineQueue {
		procLogf("[WORKER] dequeued mode=refine job_id=%s kind=%s", t.jobID, t.kind)
		jobsInProgress.Inc()
		setQueueLen()
		processTask(t, true)
		jobsInProgress.Dec()
		setQueueLen()
	}
}

func processTask(t task, splitMode bool) {
	job := getJob(t.jobID)
	if job == nil {
		return
	}

	switch t.kind {
	case taskTypeTranscribe:
		current := asString(job["status"])
		if current != statusPending && current != statusRunning {
			return
		}
		if err := taskTranscribe(t.jobID); err != nil {
			procErrf("worker.transcribe", err, "job_id=%s", t.jobID)
			return
		}
		updated := getJob(t.jobID)
		if updated == nil || asString(updated["status"]) != statusRefiningPending {
			return
		}
		if splitMode {
			enqueueRefine(t.jobID)
			procLogf("[WORKER] queued refine job_id=%s", t.jobID)
			return
		}
		finalizeRefine(t.jobID)

	case taskTypeRefine:
		current := asString(job["status"])
		if current != statusRefiningPending && current != statusRefining {
			return
		}
		finalizeRefine(t.jobID)
	}
}

func finalizeRefine(jobID string) {
	b, err := loadJobBlob(jobID, blobKindTranscript)
	if err != nil {
		procErrf("worker.loadTranscriptBlob", err, "job_id=%s", jobID)
		setJobFields(jobID, map[string]any{"status": statusFailed})
		return
	}
	if err := taskRefining(jobID, string(b)); err != nil {
		setJobFields(jobID, map[string]any{"status": statusFailed})
		procErrf("worker.refine", err, "job_id=%s", jobID)
		return
	}
	setJobFields(jobID, map[string]any{"status": statusCompleted, "result": "db://transcript"})
	procLogf("[WORKER] completed job_id=%s result=db://transcript", jobID)
}

func taskTranscribe(jobID string) error {
	procLogf("[TRANSCRIBE] start job_id=%s input=db://wav", jobID)
	started := time.Now()
	setJobFields(jobID, map[string]any{
		"status":       statusRunning,
		"started_at":   started.Format("2006-01-02 15:04:05"),
		"started_ts":   float64(started.Unix()),
		"preview_text": "",
	})

	job := getJob(jobID)
	totalSec := asIntPtr(job["media_duration_seconds"])
	wavBytes, err := loadJobBlob(jobID, blobKindWav)
	if err != nil {
		procErrf("transcribe.loadWavBlob", err, "job_id=%s", jobID)
		setJobFields(jobID, map[string]any{"status": statusFailed})
		jobsTotal.WithLabelValues("failure").Inc()
		return err
	}
	timelineText, err := runWhisperFromBlob(jobID, wavBytes, totalSec)
	if err != nil {
		statusLabel := "failure"
		fields := map[string]any{"status": statusFailed}
		if errors.Is(err, context.DeadlineExceeded) {
			fields["status_detail"] = "타임아웃"
			statusLabel = "timeout"
		}
		setJobFields(jobID, fields)
		jobsTotal.WithLabelValues(statusLabel).Inc()
		procErrf("transcribe.runWhisper", err, "job_id=%s", jobID)
		return err
	}

	if err := saveJobBlob(jobID, blobKindTranscript, []byte(timelineText)); err != nil {
		setJobFields(jobID, map[string]any{"status": statusFailed})
		jobsTotal.WithLabelValues("failure").Inc()
		procErrf("transcribe.saveTranscriptBlob", err, "job_id=%s", jobID)
		return err
	}

	completed := time.Now()
	jobsTotal.WithLabelValues("success").Inc()
	jobDurationSec.Observe(completed.Sub(started).Seconds())

	nextStatus := statusCompleted
	refineEnabled := truthy(asString(job["refine_enabled"]))
	if refineEnabled && hasGeminiConfigured() {
		nextStatus = statusRefiningPending
	}

	setJobFields(jobID, map[string]any{
		"status":       nextStatus,
		"result":       "db://transcript",
		"completed_at": completed.Format("2006-01-02 15:04:05"),
		"completed_ts": float64(completed.Unix()),
		"duration":     formatSeconds(int(completed.Sub(started).Seconds())),
	})
	deleteJobBlob(jobID, blobKindWav)
	procLogf("[TRANSCRIBE] cleaned input blob job_id=%s kind=%s", jobID, blobKindWav)
	procLogf("[TRANSCRIBE] done job_id=%s output=db://transcript status=%s duration_sec=%d", jobID, nextStatus, int(completed.Sub(started).Seconds()))
	return nil
}

func taskRefining(jobID, timelineText string) error {
	procLogf("[REFINE] start job_id=%s", jobID)
	setJobFields(jobID, map[string]any{"status": statusRefining})
	if !hasGeminiConfigured() {
		procLogf("[REFINE] skipped job_id=%s reason=no gemini key", jobID)
		return nil
	}
	job := getJob(jobID)
	desc := buildRefineDescription(job)
	refined, err := refineTranscript(timelineText, desc)
	if err != nil || strings.TrimSpace(refined) == "" {
		if err != nil {
			procErrf("refine.refineTranscript", err, "job_id=%s", jobID)
		} else {
			procLogf("[REFINE] empty result job_id=%s", jobID)
		}
		return err
	}
	if err := saveJobBlob(jobID, blobKindRefined, []byte(refined)); err != nil {
		procErrf("refine.saveRefinedBlob", err, "job_id=%s", jobID)
		return err
	}
	setJobFields(jobID, map[string]any{"result_refined": "db://refined"})
	procLogf("[REFINE] done job_id=%s output=db://refined", jobID)
	return nil
}

func buildRefineDescription(job map[string]any) string {
	base := strings.TrimSpace(asString(job["description"]))
	ownerID := strings.TrimSpace(asString(job["owner_id"]))
	tags := uniqueStringsKeepOrder(asStringSlice(job["tags"]))
	if ownerID == "" || len(tags) == 0 {
		return base
	}

	descMap, err := getTagDescriptionsByNames(ownerID, tags)
	if err != nil {
		procErrf("refine.getTagDescriptions", err, "owner_id=%s", ownerID)
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

func runWhisperFromBlob(jobID string, wavBytes []byte, totalSec *int) (string, error) {
	tmpDir, err := os.MkdirTemp("", "whisper-job-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	wavPath := filepath.Join(tmpDir, jobID+".wav")
	if err := os.WriteFile(wavPath, wavBytes, 0o644); err != nil {
		return "", err
	}
	return runWhisper(jobID, wavPath, totalSec)
}

func runWhisper(jobID, wavPath string, totalSec *int) (string, error) {
	procLogf("[WHISPER] start job_id=%s wav=%s total_sec=%v", jobID, wavPath, totalSec)
	outputPath := wavPath + ".txt"
	modelBin := filepath.Join(modelDir, "ggml-large-v3.bin")
	vadModel := filepath.Join(modelDir, "ggml-silero-v6.2.0.bin")

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(jobTimeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, whisperCLI,
		"-m", modelBin,
		"-l", "ko",
		"--max-context", "0",
		"--no-speech-thold", "0.01",
		"--suppress-nst",
		"--no-prints",
		"--vad",
		"--vad-model", vadModel,
		"--vad-threshold", "0.01",
		"--output-txt", wavPath,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		procErrf("whisper.stdoutPipe", err, "job_id=%s", jobID)
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		procErrf("whisper.stderrPipe", err, "job_id=%s", jobID)
		return "", err
	}
	if err := cmd.Start(); err != nil {
		procErrf("whisper.start", err, "job_id=%s", jobID)
		return "", err
	}

	setJobFields(jobID, map[string]any{"phase": "전처리 중", "progress_percent": 0, "progress_label": "전처리 중..."})

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
		m := progressRe.FindStringSubmatch(line)
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
		setJobFields(jobID, map[string]any{
			"phase":            "전사 중",
			"progress_percent": percent,
			"progress_label":   fmt.Sprintf("전사 중... %d%%", percent),
		})
		lastPercent = percent
		if percent > maxPercent {
			maxPercent = percent
		}
		if line != lastPreviewLine {
			appendJobPreviewLine(jobID, line)
			lastPreviewLine = line
		}
		if percent >= lastProgressLog+5 || percent == 100 {
			procLogf("[WHISPER] progress job_id=%s percent=%d", jobID, percent)
			lastProgressLog = percent
		}
		sawTimeline = true
	}

	if err := cmd.Wait(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			procErrf("whisper.wait", context.DeadlineExceeded, "job_id=%s timeout_sec=%d", jobID, jobTimeoutSec)
			return "", context.DeadlineExceeded
		}
		procErrf("whisper.wait", err, "job_id=%s", jobID)
		return "", err
	}
	if sawTimeline {
		setJobFields(jobID, map[string]any{"phase": "전사 완료", "progress_percent": 100, "progress_label": "전사 완료"})
	}

	b, err := os.ReadFile(outputPath)
	if err != nil {
		procErrf("whisper.readOutput", err, "job_id=%s path=%s", jobID, outputPath)
		return "", err
	}
	_ = os.Remove(outputPath)
	procLogf("[WHISPER] done job_id=%s", jobID)
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
