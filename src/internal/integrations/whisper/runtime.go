package whisper

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
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Config defines how the Whisper CLI adapter should be executed and observed.
type Config struct {
	ModelDir      string
	WhisperCLI    string
	JobTimeoutSec int
	ProgressRe    *regexp.Regexp
	Logf          func(string, ...any)
	Errf          func(string, error, string, ...any)
	OnPhase       func(jobID, phase string, percent int, label string)
	OnPreviewLine func(jobID, line string)
	OnPreviewText func(jobID, text string)
}

// RunResult contains the artifacts returned by a successful Whisper run.
type RunResult struct {
	TimelineText   string
	TranscriptJSON []byte
}

// Runtime adapts the external Whisper CLI to the worker-facing interface.
type Runtime struct {
	cfg Config
}

// New creates a Whisper runtime around the provided CLI configuration.
func New(cfg Config) *Runtime {
	return &Runtime{cfg: cfg}
}

// RunFromBlob materializes a temporary WAV file before invoking the Whisper CLI.
func (r *Runtime) RunFromBlob(ctx context.Context, jobID string, wavBytes []byte, totalSec *int) (RunResult, error) {
	tmpDir, err := os.MkdirTemp("", "whisper-job-*")
	if err != nil {
		return RunResult{}, err
	}
	defer os.RemoveAll(tmpDir)

	wavPath := filepath.Join(tmpDir, jobID+".wav")
	if err := os.WriteFile(wavPath, wavBytes, 0o644); err != nil {
		return RunResult{}, err
	}
	return r.Run(ctx, jobID, wavPath, totalSec)
}

// Run executes Whisper on a WAV file and collects text plus structured transcript output.
func (r *Runtime) Run(ctx context.Context, jobID, wavPath string, totalSec *int) (RunResult, error) {
	r.logf("[WHISPER] start job_id=%s wav=%s total_sec=%s", jobID, wavPath, formatTotalSec(totalSec))

	outputPath := wavPath + ".txt"
	outputJSONPath := wavPath + ".json"
	modelBin := filepath.Join(r.cfg.ModelDir, "ggml-model.bin")
	if _, err := os.Stat(modelBin); err != nil {
		r.errf("whisper.modelPath", err, "job_id=%s model_dir=%s", jobID, r.cfg.ModelDir)
		return RunResult{}, err
	}
	vadModel := filepath.Join(r.cfg.ModelDir, "ggml-silero-v6.2.0.bin")
	if _, err := os.Stat(vadModel); err != nil {
		r.errf("whisper.vadModelPath", err, "job_id=%s path=%s", jobID, vadModel)
		return RunResult{}, err
	}

	// Build the CLI command using the bundled model and VAD assets.
	cmd := exec.CommandContext(ctx, r.cfg.WhisperCLI,
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
		r.errf("whisper.stdoutPipe", err, "job_id=%s", jobID)
		return RunResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.errf("whisper.stderrPipe", err, "job_id=%s", jobID)
		return RunResult{}, err
	}
	if err := cmd.Start(); err != nil {
		r.errf("whisper.start", err, "job_id=%s", jobID)
		return RunResult{}, err
	}

	r.phase(jobID, "전처리 중", 0, "전처리 중...")

	// Read stdout and stderr together because progress lines can appear on either stream.
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

	// Parse timeline output to update progress and preview text incrementally.
	for line := range lines {
		lastDiagnosticLine = line
		if !strings.Contains(line, "-->") {
			continue
		}
		m := r.cfg.ProgressRe.FindStringSubmatch(line)
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
			r.previewText(jobID, string(previewBytes))
		} else {
			r.previewLine(jobID, line)
		}
		if percent == lastPercent {
			sawTimeline = true
			continue
		}
		r.phase(jobID, "전사 중", percent, fmt.Sprintf("전사 중... %d%%", percent))
		lastPercent = percent
		if percent > maxPercent {
			maxPercent = percent
		}
		if percent >= lastProgressLog+5 || percent == 100 {
			r.logf("[WHISPER] progress job_id=%s percent=%d", jobID, percent)
			lastProgressLog = percent
		}
		sawTimeline = true
	}

	// Finish the process and translate known timeout/error cases.
	if err := cmd.Wait(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			r.errf("whisper.wait", context.DeadlineExceeded, "job_id=%s timeout_sec=%d", jobID, r.cfg.JobTimeoutSec)
			return RunResult{}, context.DeadlineExceeded
		}
		if strings.TrimSpace(lastDiagnosticLine) != "" {
			r.errf("whisper.wait", err, "job_id=%s stderr=%s", jobID, lastDiagnosticLine)
		} else {
			r.errf("whisper.wait", err, "job_id=%s", jobID)
		}
		return RunResult{}, err
	}
	if sawTimeline {
		r.phase(jobID, "전사 완료", 100, "전사 완료")
	}

	// Load the final text and JSON artifacts that Whisper wrote beside the WAV file.
	b, err := os.ReadFile(outputPath)
	if err != nil {
		r.errf("whisper.readOutput", err, "job_id=%s path=%s", jobID, outputPath)
		return RunResult{}, err
	}
	timelineText, err := buildTimelineTranscriptText(outputJSONPath)
	if err != nil {
		r.errf("whisper.buildTimelineTranscript", err, "job_id=%s path=%s", jobID, outputJSONPath)
		return RunResult{}, err
	}
	slimJSON, err := buildSlimTranscriptJSON(outputJSONPath)
	if err != nil {
		r.errf("whisper.readOutputJSON", err, "job_id=%s path=%s", jobID, outputJSONPath)
		return RunResult{}, err
	}
	r.previewText(jobID, string(b))
	_ = os.Remove(outputPath)
	_ = os.Remove(outputJSONPath)
	r.logf("[WHISPER] done job_id=%s", jobID)
	return RunResult{TimelineText: timelineText, TranscriptJSON: slimJSON}, nil
}

// logf emits integration logs when configured.
func (r *Runtime) logf(format string, args ...any) {
	if r.cfg.Logf != nil {
		r.cfg.Logf(format, args...)
	}
}

// errf emits integration errors when configured.
func (r *Runtime) errf(scope string, err error, format string, args ...any) {
	if r.cfg.Errf != nil {
		r.cfg.Errf(scope, err, format, args...)
	}
}

// phase forwards progress updates to the worker runtime.
func (r *Runtime) phase(jobID, phase string, percent int, label string) {
	if r.cfg.OnPhase != nil {
		r.cfg.OnPhase(jobID, phase, percent, label)
	}
}

// previewLine forwards a single raw progress line to the worker runtime.
func (r *Runtime) previewLine(jobID, line string) {
	if r.cfg.OnPreviewLine != nil {
		r.cfg.OnPreviewLine(jobID, line)
	}
}

// previewText forwards accumulated preview text to the worker runtime.
func (r *Runtime) previewText(jobID, text string) {
	if r.cfg.OnPreviewText != nil {
		r.cfg.OnPreviewText(jobID, text)
	}
}

// formatTotalSec renders the optional total duration for logs.
func formatTotalSec(totalSec *int) string {
	if totalSec == nil {
		return "unknown"
	}
	return strconv.Itoa(*totalSec)
}

// scanPipe tokenizes one CLI output stream into trimmed lines.
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

// splitOnCRLF treats both `\r` and `\n` as line boundaries.
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

// whisperJSONOutput is the raw JSON shape written by the Whisper CLI.
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

// slimTranscriptJSON is the smaller JSON shape persisted by this application.
type slimTranscriptJSON struct {
	Segments []slimTranscriptSegment `json:"segments"`
}

// slimTranscriptSegment is one persisted transcript segment.
type slimTranscriptSegment struct {
	From string `json:"from"`
	To   string `json:"to"`
	Text string `json:"text"`
}

// buildSlimTranscriptJSON converts raw Whisper JSON into the persisted compact form.
func buildSlimTranscriptJSON(path string) ([]byte, error) {
	segments, err := loadTranscriptSegments(path)
	if err != nil {
		return nil, err
	}
	out := slimTranscriptJSON{Segments: make([]slimTranscriptSegment, 0, len(segments))}
	for _, item := range segments {
		out.Segments = append(out.Segments, slimTranscriptSegment{
			From: item.Timestamps.From,
			To:   item.Timestamps.To,
			Text: strings.TrimSpace(item.Text),
		})
	}
	return json.Marshal(out)
}

// buildTimelineTranscriptText renders a simple `time : "text"` transcript view.
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
		lines = append(lines, fmt.Sprintf(`%s : "%s"`, item.Timestamps.From, text))
	}
	return strings.Join(lines, "\n"), nil
}

// loadTranscriptSegments loads raw transcript segments from Whisper JSON output.
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
