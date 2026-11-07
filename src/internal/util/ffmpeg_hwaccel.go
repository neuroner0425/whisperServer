package util

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Hardware acceleration profiles recognized by the pipeline.
const (
	ProfileVideotoolbox     = "videotoolbox"
	ProfileCUDA             = "cuda"
	ProfileVAAPI            = "vaapi"
	ProfileSoftware         = "software"
	ProfileSoftwareFallback = "software_fallback"
)

// HWProfile defines the hardware acceleration parameters and audio encoder for FFmpeg commands.
type HWProfile struct {
	Name        string
	HwaccelArgs []string
	AACEncoder  string
}

// IsHardwareAccelerated reports whether the profile utilizes hardware decoding or specialized encoding.
func (p HWProfile) IsHardwareAccelerated() bool {
	return len(p.HwaccelArgs) > 0 || (p.AACEncoder != "" && p.AACEncoder != "aac") || (p.Name != "" && p.Name != ProfileSoftware)
}

// HwaccelReport captures telemetry about the execution profile and timing.
type HwaccelReport struct {
	ProfileUsed  string
	FallbackUsed bool
	Elapsed      time.Duration
}

var (
	probeOnce      sync.Once
	cachedProfile  HWProfile
	execCommand    = exec.Command
	execCommandCtx = exec.CommandContext
)

// GetHWProfile returns the probed hardware acceleration profile, caching the result thread-safely.
func GetHWProfile() HWProfile {
	probeOnce.Do(func() {
		cachedProfile = ProbeHWProfile()
	})
	return cachedProfile
}

// ProbeHWProfile queries FFmpeg to discover available hardware acceleration methods and encoders.
func ProbeHWProfile() HWProfile {
	hwOutput, err := execCommand("ffmpeg", "-hide_banner", "-hwaccels").Output()
	if err != nil {
		return HWProfile{
			Name:        ProfileSoftware,
			HwaccelArgs: nil,
			AACEncoder:  "aac",
		}
	}

	encOutput, err := execCommand("ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		return HWProfile{
			Name:        ProfileSoftware,
			HwaccelArgs: nil,
			AACEncoder:  "aac",
		}
	}

	hwaccels := parseHWAccels(string(hwOutput))
	encoders := parseEncoders(string(encOutput))
	return determineHWProfile(hwaccels, encoders, runtime.GOOS)
}

func parseHWAccels(output string) map[string]bool {
	supported := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(strings.ToLower(line), "hardware acceleration") || strings.HasPrefix(line, "-") {
			continue
		}
		for _, field := range strings.Fields(line) {
			supported[strings.ToLower(field)] = true
		}
	}
	return supported
}

func parseEncoders(output string) map[string]bool {
	supported := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && len(fields[0]) == 6 && fields[1] != "=" {
			supported[strings.ToLower(fields[1])] = true
		}
	}
	return supported
}

func determineHWProfile(hwaccels map[string]bool, encoders map[string]bool, targetOS string) HWProfile {
	switch targetOS {
	case "darwin":
		if hwaccels["videotoolbox"] {
			aacEnc := "aac"
			if encoders["aac_at"] {
				aacEnc = "aac_at"
			}
			return HWProfile{
				Name:        ProfileVideotoolbox,
				HwaccelArgs: []string{"-hwaccel", "videotoolbox"},
				AACEncoder:  aacEnc,
			}
		}
	case "linux":
		if hwaccels["cuda"] || hwaccels["nvdec"] {
			hwMethod := "cuda"
			if !hwaccels["cuda"] && hwaccels["nvdec"] {
				hwMethod = "nvdec"
			}
			return HWProfile{
				Name:        ProfileCUDA,
				HwaccelArgs: []string{"-hwaccel", hwMethod},
				AACEncoder:  "aac",
			}
		}
		if hwaccels["vaapi"] {
			return HWProfile{
				Name:        ProfileVAAPI,
				HwaccelArgs: []string{"-hwaccel", "vaapi"},
				AACEncoder:  "aac",
			}
		}
	}

	return HWProfile{
		Name:        ProfileSoftware,
		HwaccelArgs: nil,
		AACEncoder:  "aac",
	}
}

func buildDualOutputArgs(rawInputPath, outWavPath, outAACPath string, hwaccelArgs []string, aacEncoder string) []string {
	if aacEncoder == "" {
		aacEncoder = "aac"
	}
	args := []string{"-hide_banner", "-y"}
	if len(hwaccelArgs) > 0 {
		args = append(args, hwaccelArgs...)
	}
	args = append(args,
		"-i", rawInputPath,
		"-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", outWavPath,
		"-vn", "-c:a", aacEncoder, "-b:a", "128k", "-movflags", "+faststart", outAACPath,
	)
	return args
}

// ExtractSTTWavAndPlaybackAAC runs a single-pass dual-output FFmpeg conversion generating both a mono 16kHz
// WAV suitable for STT and a faststart AAC track for playback. If hardware acceleration fails, it automatically
// falls back to software decoding/encoding.
func ExtractSTTWavAndPlaybackAAC(ctx context.Context, rawInputPath, outWavPath, outAACPath string) (HwaccelReport, error) {
	return ExtractSTTWavAndPlaybackAACWithProfile(ctx, rawInputPath, outWavPath, outAACPath, GetHWProfile())
}

// ExtractSTTWavAndPlaybackAACWithProfile executes the dual-output conversion using a specific profile,
// falling back to software mode if the initial hardware-accelerated pass fails.
func ExtractSTTWavAndPlaybackAACWithProfile(ctx context.Context, rawInputPath, outWavPath, outAACPath string, profile HWProfile) (HwaccelReport, error) {
	if rawInputPath == "" {
		return HwaccelReport{}, errors.New("rawInputPath is required")
	}
	if outWavPath == "" {
		return HwaccelReport{}, errors.New("outWavPath is required")
	}
	if outAACPath == "" {
		return HwaccelReport{}, errors.New("outAACPath is required")
	}

	if _, err := os.Stat(rawInputPath); err != nil {
		return HwaccelReport{}, fmt.Errorf("raw input file not accessible: %w", err)
	}

	if dir := filepath.Dir(outWavPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return HwaccelReport{}, fmt.Errorf("failed to create wav output dir: %w", err)
		}
	}
	if dir := filepath.Dir(outAACPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return HwaccelReport{}, fmt.Errorf("failed to create aac output dir: %w", err)
		}
	}

	start := time.Now()
	args := buildDualOutputArgs(rawInputPath, outWavPath, outAACPath, profile.HwaccelArgs, profile.AACEncoder)
	cmd := execCommandCtx(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return HwaccelReport{
			ProfileUsed:  profile.Name,
			FallbackUsed: false,
			Elapsed:      time.Since(start),
		}, nil
	}

	// Check context cancellation first - do not fallback if cancelled by caller
	if ctx.Err() != nil {
		return HwaccelReport{
			ProfileUsed:  profile.Name,
			FallbackUsed: false,
			Elapsed:      time.Since(start),
		}, ctx.Err()
	}

	// If hardware acceleration was not enabled, no fallback is possible
	if !profile.IsHardwareAccelerated() {
		_ = os.Remove(outWavPath)
		_ = os.Remove(outAACPath)
		return HwaccelReport{
			ProfileUsed:  profile.Name,
			FallbackUsed: false,
			Elapsed:      time.Since(start),
		}, fmt.Errorf("ffmpeg software conversion failed: %w | stderr=%s", err, strings.TrimSpace(stderr.String()))
	}

	// Defensive fallback: retry with pure software mode (hwaccel=none, c:a=aac)
	swArgs := buildDualOutputArgs(rawInputPath, outWavPath, outAACPath, nil, "aac")
	swCmd := execCommandCtx(ctx, "ffmpeg", swArgs...)
	var swStderr bytes.Buffer
	swCmd.Stderr = &swStderr

	swErr := swCmd.Run()
	if swErr != nil {
		_ = os.Remove(outWavPath)
		_ = os.Remove(outAACPath)
		return HwaccelReport{
			ProfileUsed:  ProfileSoftwareFallback,
			FallbackUsed: true,
			Elapsed:      time.Since(start),
		}, fmt.Errorf("ffmpeg hwaccel attempt (%s) failed: %v; software fallback failed: %w | stderr=%s", profile.Name, err, swErr, strings.TrimSpace(swStderr.String()))
	}

	return HwaccelReport{
		ProfileUsed:  ProfileSoftwareFallback,
		FallbackUsed: true,
		Elapsed:      time.Since(start),
	}, nil
}
