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
	"strconv"
	"strings"
	"sync"
)

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
