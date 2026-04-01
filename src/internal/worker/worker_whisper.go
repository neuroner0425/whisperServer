package worker

import (
	"bufio"
	"context"
	"encoding/json"
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

func (w *Worker) runWhisperFromBlob(ctx context.Context, jobID string, wavBytes []byte, totalSec *int) (string, []byte, error) {
	tmpDir, err := os.MkdirTemp("", "whisper-job-*")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(tmpDir)

	wavPath := filepath.Join(tmpDir, jobID+".wav")
	if err := os.WriteFile(wavPath, wavBytes, 0o644); err != nil {
		return "", nil, err
	}
	return w.runWhisper(ctx, jobID, wavPath, totalSec)
}

func (w *Worker) runWhisper(ctx context.Context, jobID, wavPath string, totalSec *int) (string, []byte, error) {
	w.deps.Logf("[WHISPER] start job_id=%s wav=%s total_sec=%s", jobID, wavPath, formatTotalSec(totalSec))
	outputPath := wavPath + ".txt"
	outputJSONPath := wavPath + ".json"
	modelBin := filepath.Join(w.cfg.ModelDir, "ggml-model.bin")
	if _, err := os.Stat(modelBin); err != nil {
		w.deps.Errf("whisper.modelPath", err, "job_id=%s model_dir=%s", jobID, w.cfg.ModelDir)
		return "", nil, err
	}
	vadModel := filepath.Join(w.cfg.ModelDir, "ggml-silero-v6.2.0.bin")
	if _, err := os.Stat(vadModel); err != nil {
		w.deps.Errf("whisper.vadModelPath", err, "job_id=%s path=%s", jobID, vadModel)
		return "", nil, err
	}

	cmd := exec.CommandContext(ctx, w.cfg.WhisperCLI,
		"-m", modelBin,
		"-l", "ko",
		"--max-context", "0",
		"--no-speech-thold", "0.01",
		"--suppress-nst",
		"--vad",
		"--vad-model", vadModel,
		"--vad-threshold", "0.01",
		"--output-txt",
		"--output-json",
		wavPath,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		w.deps.Errf("whisper.stdoutPipe", err, "job_id=%s", jobID)
		return "", nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		w.deps.Errf("whisper.stderrPipe", err, "job_id=%s", jobID)
		return "", nil, err
	}
	if err := cmd.Start(); err != nil {
		w.deps.Errf("whisper.start", err, "job_id=%s", jobID)
		return "", nil, err
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
	sawTimeline := false
	lastDiagnosticLine := ""

	for line := range lines {
		lastDiagnosticLine = line
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
		if previewBytes, readErr := os.ReadFile(outputPath); readErr == nil && len(previewBytes) > 0 {
			w.deps.ReplaceJobPreviewText(jobID, string(previewBytes))
		} else {
			w.deps.AppendJobPreviewLine(jobID, line)
		}
		if percent == lastPercent {
			sawTimeline = true
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
		if percent >= lastProgressLog+5 || percent == 100 {
			w.deps.Logf("[WHISPER] progress job_id=%s percent=%d", jobID, percent)
			lastProgressLog = percent
		}
		sawTimeline = true
	}

	if err := cmd.Wait(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			w.deps.Errf("whisper.wait", context.DeadlineExceeded, "job_id=%s timeout_sec=%d", jobID, w.cfg.JobTimeoutSec)
			return "", nil, context.DeadlineExceeded
		}
		if strings.TrimSpace(lastDiagnosticLine) != "" {
			w.deps.Errf("whisper.wait", err, "job_id=%s stderr=%s", jobID, lastDiagnosticLine)
		} else {
			w.deps.Errf("whisper.wait", err, "job_id=%s", jobID)
		}
		return "", nil, err
	}
	if sawTimeline {
		w.deps.SetJobFields(jobID, map[string]any{"phase": "전사 완료", "progress_percent": 100, "progress_label": "전사 완료"})
	}

	b, err := os.ReadFile(outputPath)
	if err != nil {
		w.deps.Errf("whisper.readOutput", err, "job_id=%s path=%s", jobID, outputPath)
		return "", nil, err
	}
	timelineText, err := buildTimelineTranscriptText(outputJSONPath)
	if err != nil {
		w.deps.Errf("whisper.buildTimelineTranscript", err, "job_id=%s path=%s", jobID, outputJSONPath)
		return "", nil, err
	}
	slimJSON, err := buildSlimTranscriptJSON(outputJSONPath)
	if err != nil {
		w.deps.Errf("whisper.readOutputJSON", err, "job_id=%s path=%s", jobID, outputJSONPath)
		return "", nil, err
	}
	w.deps.ReplaceJobPreviewText(jobID, string(b))
	_ = os.Remove(outputPath)
	_ = os.Remove(outputJSONPath)
	w.deps.Logf("[WHISPER] done job_id=%s", jobID)
	return timelineText, slimJSON, nil
}

func formatTotalSec(totalSec *int) string {
	if totalSec == nil {
		return "unknown"
	}
	return strconv.Itoa(*totalSec)
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

type whisperJSONOutput struct {
	Transcription []struct {
		Timestamps struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"timestamps"`
		Offsets struct {
			From int `json:"from"`
			To   int `json:"to"`
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

type slimTranscriptJSON struct {
	Segments []slimTranscriptSegment `json:"segments"`
}

type slimTranscriptSegment struct {
	From string `json:"from"`
	To   string `json:"to"`
	Text string `json:"text"`
}

func buildSlimTranscriptJSON(path string) ([]byte, error) {
	segments, err := loadTranscriptSegments(path)
	if err != nil {
		return nil, err
	}
	out := slimTranscriptJSON{
		Segments: make([]slimTranscriptSegment, 0, len(segments)),
	}
	for _, item := range segments {
		out.Segments = append(out.Segments, slimTranscriptSegment{
			From: item.Timestamps.From,
			To:   item.Timestamps.To,
			Text: strings.TrimSpace(item.Text),
		})
	}
	return json.Marshal(out)
}

func buildTimelineTranscriptText(path string) (string, error) {
	segments, err := loadTranscriptSegments(path)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(segments))
	for _, item := range segments {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf(`%s ~ %s "%s"`, item.Timestamps.From, item.Timestamps.To, text))
	}
	return strings.Join(lines, "\n"), nil
}

func loadTranscriptSegments(path string) ([]struct {
	Timestamps struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"timestamps"`
	Offsets struct {
		From int `json:"from"`
		To   int `json:"to"`
	} `json:"offsets"`
	Text string `json:"text"`
}, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var source whisperJSONOutput
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	return source.Transcription, nil
}
