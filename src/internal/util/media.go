package util

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrUploadTooLarge reports that a streamed upload exceeded the configured limit.
var ErrUploadTooLarge = errors.New("upload too large")

// wavTranscriptionFilter is the default ffmpeg filter chain used before Whisper.
const wavTranscriptionFilter = "highpass=f=80,lowpass=f=7000,dynaudnorm=f=150:g=15:p=0.95:m=10,alimiter=limit=0.95"

const (
	phaseDetectSampleRate       = 16000
	phaseDetectWindowFrames     = phaseDetectSampleRate
	phaseDetectMinActiveDB      = -50.0
	phaseDetectMaxCorrelation   = -0.8
	phaseDetectMinSideOverMidDB = 12.0
	phaseDetectMinFlaggedRatio  = 0.15
	phaseDetectMinFlaggedSecs   = 3
)

// PhaseCancellationReport summarizes stereo phase-cancellation risk for an audio file.
type PhaseCancellationReport struct {
	Windows              int
	ActiveWindows        int
	FlaggedWindows       int
	FlaggedRatio         float64
	GlobalCorrelation    float64
	GlobalLeftDB         float64
	GlobalRightDB        float64
	GlobalMidDB          float64
	GlobalSideDB         float64
	GlobalSideMinusMidDB float64
	PreferredMonoChannel int
	ShouldUseChannelMono bool
}

// DetectFileType classifies uploads using only their filename extension.
func DetectFileType(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	switch ext {
	case "mp3", "wav", "m4a":
		return "audio"
	case "pdf":
		return "pdf"
	case "ppt", "pptx":
		return "ppt"
	default:
		return "unknown"
	}
}

// AllowedFile checks whether the upload extension is explicitly allowed.
func AllowedFile(name string, allowedExtensions map[string]struct{}) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	_, ok := allowedExtensions[ext]
	return ok
}

// SaveUploadWithLimit streams an upload to disk with size and optional rate limits.
func SaveUploadWithLimit(h *multipart.FileHeader, dst string, maxBytes int64, chunkSize int, bytesPerSec int64) (int64, error) {
	src, err := h.Open()
	if err != nil {
		return 0, err
	}
	defer src.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	buf := make([]byte, chunkSize)
	var written int64
	startedAt := time.Now()
	// Copy in chunks so size checks and throttling can be enforced incrementally.
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxBytes {
				return written, ErrUploadTooLarge
			}
			if _, err := out.Write(buf[:n]); err != nil {
				return written, err
			}
			if bytesPerSec > 0 {
				expectedElapsed := time.Duration(float64(written) / float64(bytesPerSec) * float64(time.Second))
				if sleepFor := time.Until(startedAt.Add(expectedElapsed)); sleepFor > 0 {
					time.Sleep(sleepFor)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return written, readErr
		}
	}
	return written, nil
}

// ConvertToWav normalizes arbitrary media into the mono 16k WAV expected by Whisper.
func ConvertToWav(src, dst string) error {
	if report, err := DetectPhaseCancellation(src); err == nil && report.ShouldUseChannelMono {
		return convertToWavUsingChannel(src, dst, report.PreferredMonoChannel)
	}
	return convertToWavUsingNormalDownmix(src, dst)
}

func convertToWavUsingNormalDownmix(src, dst string) error {
	filteredArgs := []string{
		"-y",
		"-i", src,
		"-vn",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		"-af", wavTranscriptionFilter,
		dst,
	}
	// Try the filtered conversion first, then fall back to a plain resample when filters fail.
	if out, err := runFFmpeg(filteredArgs...); err == nil {
		return nil
	} else {
		plainArgs := []string{
			"-y",
			"-i", src,
			"-vn",
			"-ac", "1",
			"-ar", "16000",
			"-c:a", "pcm_s16le",
			dst,
		}
		if fallbackOut, fallbackErr := runFFmpeg(plainArgs...); fallbackErr == nil {
			return nil
		} else {
			return fmt.Errorf("ffmpeg filtered conversion failed: %s; fallback failed: %s", out, fallbackOut)
		}
	}
}

func convertToWavUsingChannel(src, dst string, channel int) error {
	if channel != 1 {
		channel = 0
	}
	channelFilter := fmt.Sprintf("pan=mono|c0=c%d", channel)
	filteredArgs := []string{
		"-y",
		"-i", src,
		"-vn",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		"-af", channelFilter + "," + wavTranscriptionFilter,
		dst,
	}
	if out, err := runFFmpeg(filteredArgs...); err == nil {
		return nil
	} else {
		plainArgs := []string{
			"-y",
			"-i", src,
			"-vn",
			"-ar", "16000",
			"-c:a", "pcm_s16le",
			"-af", channelFilter,
			dst,
		}
		if fallbackOut, fallbackErr := runFFmpeg(plainArgs...); fallbackErr == nil {
			return nil
		} else {
			return fmt.Errorf("ffmpeg channel conversion failed: %s; fallback failed: %s", out, fallbackOut)
		}
	}
}

// DetectPhaseCancellation scans stereo PCM windows for destructive L+R mono downmix risk.
func DetectPhaseCancellation(src string) (PhaseCancellationReport, error) {
	cmd := exec.Command(
		"ffmpeg",
		"-hide_banner",
		"-nostats",
		"-v", "error",
		"-i", src,
		"-vn",
		"-f", "f32le",
		"-acodec", "pcm_f32le",
		"-ac", "2",
		"-ar", strconv.Itoa(phaseDetectSampleRate),
		"-",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return PhaseCancellationReport{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return PhaseCancellationReport{}, err
	}

	detector := newPhaseCancellationDetector()
	buf := make([]byte, 64*1024)
	carry := make([]byte, 0, 8)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if len(carry) > 0 {
				merged := make([]byte, 0, len(carry)+len(chunk))
				merged = append(merged, carry...)
				merged = append(merged, chunk...)
				chunk = merged
				carry = carry[:0]
			}
			full := len(chunk) - len(chunk)%8
			if full > 0 {
				detector.writeFrames(chunk[:full])
			}
			if full < len(chunk) {
				carry = append(carry, chunk[full:]...)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = cmd.Wait()
			return PhaseCancellationReport{}, readErr
		}
	}
	if err := cmd.Wait(); err != nil {
		return PhaseCancellationReport{}, fmt.Errorf("ffmpeg phase scan failed: %w | output=%s", err, strings.TrimSpace(stderr.String()))
	}
	return detector.buildReport(), nil
}

type phaseCancellationDetector struct {
	window phaseStats
	global phaseStats
	report PhaseCancellationReport
}

type phaseStats struct {
	n         int
	sumL      float64
	sumR      float64
	sumLL     float64
	sumRR     float64
	sumLR     float64
	sumMidSq  float64
	sumSideSq float64
}

type phaseMetrics struct {
	correlation    float64
	leftDB         float64
	rightDB        float64
	midDB          float64
	sideDB         float64
	sideMinusMidDB float64
}

func newPhaseCancellationDetector() *phaseCancellationDetector {
	return &phaseCancellationDetector{report: PhaseCancellationReport{PreferredMonoChannel: 0}}
}

func (d *phaseCancellationDetector) writeFrames(data []byte) {
	for off := 0; off+7 < len(data); off += 8 {
		left := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4])))
		right := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[off+4 : off+8])))
		d.window.add(left, right)
		d.global.add(left, right)
		if d.window.n >= phaseDetectWindowFrames {
			d.finishWindow()
		}
	}
}

func (d *phaseCancellationDetector) finishWindow() {
	if d.window.n == 0 {
		return
	}
	metrics := d.window.metrics()
	d.report.Windows++
	active := metrics.midDB > phaseDetectMinActiveDB || metrics.sideDB > phaseDetectMinActiveDB
	if active {
		d.report.ActiveWindows++
	}
	if active &&
		metrics.correlation < phaseDetectMaxCorrelation &&
		metrics.sideDB > phaseDetectMinActiveDB &&
		metrics.sideMinusMidDB > phaseDetectMinSideOverMidDB {
		d.report.FlaggedWindows++
	}
	d.window = phaseStats{}
}

func (d *phaseCancellationDetector) buildReport() PhaseCancellationReport {
	d.finishWindow()
	report := d.report
	if report.ActiveWindows > 0 {
		report.FlaggedRatio = float64(report.FlaggedWindows) / float64(report.ActiveWindows)
	}
	global := d.global.metrics()
	report.GlobalCorrelation = global.correlation
	report.GlobalLeftDB = global.leftDB
	report.GlobalRightDB = global.rightDB
	report.GlobalMidDB = global.midDB
	report.GlobalSideDB = global.sideDB
	report.GlobalSideMinusMidDB = global.sideMinusMidDB
	if report.GlobalRightDB > report.GlobalLeftDB+1 {
		report.PreferredMonoChannel = 1
	}
	report.ShouldUseChannelMono =
		(report.FlaggedWindows >= phaseDetectMinFlaggedSecs || report.FlaggedWindows == report.ActiveWindows) &&
			report.FlaggedRatio >= phaseDetectMinFlaggedRatio &&
			report.GlobalSideMinusMidDB >= phaseDetectMinSideOverMidDB/2
	return report
}

func (s *phaseStats) add(left, right float64) {
	mid := (left + right) / 2
	side := (left - right) / 2
	s.n++
	s.sumL += left
	s.sumR += right
	s.sumLL += left * left
	s.sumRR += right * right
	s.sumLR += left * right
	s.sumMidSq += mid * mid
	s.sumSideSq += side * side
}

func (s phaseStats) metrics() phaseMetrics {
	if s.n == 0 {
		return phaseMetrics{leftDB: negInfDB(), rightDB: negInfDB(), midDB: negInfDB(), sideDB: negInfDB()}
	}
	n := float64(s.n)
	leftMean := s.sumL / n
	rightMean := s.sumR / n
	leftVariance := s.sumLL/n - leftMean*leftMean
	rightVariance := s.sumRR/n - rightMean*rightMean
	correlation := 0.0
	if leftVariance > 0 && rightVariance > 0 {
		correlation = (s.sumLR/n - leftMean*rightMean) / math.Sqrt(leftVariance*rightVariance)
	}
	leftRMS := math.Sqrt(s.sumLL / n)
	rightRMS := math.Sqrt(s.sumRR / n)
	midRMS := math.Sqrt(s.sumMidSq / n)
	sideRMS := math.Sqrt(s.sumSideSq / n)
	midDB := rmsDB(midRMS)
	sideDB := rmsDB(sideRMS)
	return phaseMetrics{
		correlation:    correlation,
		leftDB:         rmsDB(leftRMS),
		rightDB:        rmsDB(rightRMS),
		midDB:          midDB,
		sideDB:         sideDB,
		sideMinusMidDB: sideDB - midDB,
	}
}

func rmsDB(rms float64) float64 {
	if rms <= 0 {
		return negInfDB()
	}
	return 20 * math.Log10(rms)
}

func negInfDB() float64 { return -240 }

// ConvertToAac creates the AAC derivative kept for replay and retries.
func ConvertToAac(src, dst string) error {
	cmd := exec.Command(
		"ffmpeg",
		"-y",
		"-i", src,
		"-vn",
		"-c:a", "aac",
		"-b:a", "192k",
		"-ar", "48000",
		dst,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w | output=%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runFFmpeg executes ffmpeg and returns trimmed output for logging.
func runFFmpeg(args ...string) (string, error) {
	cmd := exec.Command("ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		return trimmed, fmt.Errorf("%w | output=%s", err, trimmed)
	}
	return strings.TrimSpace(string(out)), nil
}

// GetMediaDuration returns rounded seconds using ffprobe when available.
func GetMediaDuration(path string) *int {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(strings.Split(s, "\n")[0], 64)
	if err != nil {
		return nil
	}
	v := int(math.Round(f))
	return &v
}

// FormatSecondsPtr renders an optional duration for display.
func FormatSecondsPtr(sec *int) string {
	if sec == nil {
		return "-"
	}
	return FormatSeconds(*sec)
}

// FormatSeconds renders a duration in `MM:SS` or `H:MM:SS` form.
func FormatSeconds(sec int) string {
	h := sec / 3600
	r := sec % 3600
	m := r / 60
	s := r % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// SortedExts returns allowed extensions in stable order for UI display.
func SortedExts(allowedExtensions map[string]struct{}) []string {
	out := make([]string, 0, len(allowedExtensions))
	for k := range allowedExtensions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
