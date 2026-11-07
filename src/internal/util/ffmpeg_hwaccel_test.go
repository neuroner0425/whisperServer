package util

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestParseHWAccels(t *testing.T) {
	rawOutput := `Hardware acceleration methods:
videotoolbox
cuda
vaapi
qsv
`
	hw := parseHWAccels(rawOutput)
	expected := []string{"videotoolbox", "cuda", "vaapi", "qsv"}
	for _, exp := range expected {
		if !hw[exp] {
			t.Errorf("expected hwaccel %q to be present in parsed map", exp)
		}
	}
	if hw["random_method"] {
		t.Errorf("unexpected method present in parsed map")
	}
}

func TestParseEncoders(t *testing.T) {
	rawOutput := `Encoders:
 V..... = Video
 A..... = Audio
 S..... = Subtitle
 .F.... = Frame-level multithreading
 ..S... = Slice-level multithreading
 ...X.. = Codec is experimental
 ....B. = Supports draw_horiz_band
 .....D = Supports direct rendering method 1
 ------
 V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (codec h264)
 A....D aac                  AAC (Advanced Audio Coding)
 A..... aac_at               aac (AudioToolbox) (codec aac)
 A....D pcm_s16le            PCM signed 16-bit little-endian
`
	encoders := parseEncoders(rawOutput)
	expected := []string{"libx264", "aac", "aac_at", "pcm_s16le"}
	for _, exp := range expected {
		if !encoders[exp] {
			t.Errorf("expected encoder %q to be present in parsed map", exp)
		}
	}
	if encoders["="] || encoders["Video"] {
		t.Errorf("legend entries should not be parsed as encoders")
	}
}

func TestDetermineHWProfile(t *testing.T) {
	tests := []struct {
		name         string
		hwaccels     map[string]bool
		encoders     map[string]bool
		targetOS     string
		wantProfile  string
		wantAACEnc   string
		wantHwaccels int
	}{
		{
			name:         "Darwin with videotoolbox and aac_at",
			hwaccels:     map[string]bool{"videotoolbox": true},
			encoders:     map[string]bool{"aac_at": true, "aac": true},
			targetOS:     "darwin",
			wantProfile:  ProfileVideotoolbox,
			wantAACEnc:   "aac_at",
			wantHwaccels: 2,
		},
		{
			name:         "Darwin with videotoolbox without aac_at",
			hwaccels:     map[string]bool{"videotoolbox": true},
			encoders:     map[string]bool{"aac": true},
			targetOS:     "darwin",
			wantProfile:  ProfileVideotoolbox,
			wantAACEnc:   "aac",
			wantHwaccels: 2,
		},
		{
			name:         "Darwin without videotoolbox",
			hwaccels:     map[string]bool{},
			encoders:     map[string]bool{"aac": true},
			targetOS:     "darwin",
			wantProfile:  ProfileSoftware,
			wantAACEnc:   "aac",
			wantHwaccels: 0,
		},
		{
			name:         "Linux with cuda",
			hwaccels:     map[string]bool{"cuda": true, "vaapi": true},
			encoders:     map[string]bool{"aac": true},
			targetOS:     "linux",
			wantProfile:  ProfileCUDA,
			wantAACEnc:   "aac",
			wantHwaccels: 2,
		},
		{
			name:         "Linux with nvdec only",
			hwaccels:     map[string]bool{"nvdec": true},
			encoders:     map[string]bool{"aac": true},
			targetOS:     "linux",
			wantProfile:  ProfileCUDA,
			wantAACEnc:   "aac",
			wantHwaccels: 2,
		},
		{
			name:         "Linux with vaapi only",
			hwaccels:     map[string]bool{"vaapi": true},
			encoders:     map[string]bool{"aac": true},
			targetOS:     "linux",
			wantProfile:  ProfileVAAPI,
			wantAACEnc:   "aac",
			wantHwaccels: 2,
		},
		{
			name:         "Linux without hwaccels",
			hwaccels:     map[string]bool{},
			encoders:     map[string]bool{"aac": true},
			targetOS:     "linux",
			wantProfile:  ProfileSoftware,
			wantAACEnc:   "aac",
			wantHwaccels: 0,
		},
		{
			name:         "Windows or other OS fallback",
			hwaccels:     map[string]bool{"d3d11va": true},
			encoders:     map[string]bool{"aac": true},
			targetOS:     "windows",
			wantProfile:  ProfileSoftware,
			wantAACEnc:   "aac",
			wantHwaccels: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := determineHWProfile(tt.hwaccels, tt.encoders, tt.targetOS)
			if profile.Name != tt.wantProfile {
				t.Errorf("determineHWProfile().Name = %v, want %v", profile.Name, tt.wantProfile)
			}
			if profile.AACEncoder != tt.wantAACEnc {
				t.Errorf("determineHWProfile().AACEncoder = %v, want %v", profile.AACEncoder, tt.wantAACEnc)
			}
			if len(profile.HwaccelArgs) != tt.wantHwaccels {
				t.Errorf("determineHWProfile().HwaccelArgs len = %v, want %v", len(profile.HwaccelArgs), tt.wantHwaccels)
			}
		})
	}
}

func TestGetHWProfile_Concurrent(t *testing.T) {
	var wg sync.WaitGroup
	first := GetHWProfile()
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := GetHWProfile()
			if p.Name != first.Name || p.AACEncoder != first.AACEncoder {
				t.Errorf("concurrent GetHWProfile returned inconsistent result: got %+v, want %+v", p, first)
			}
		}()
	}
	wg.Wait()
}

func TestBuildDualOutputArgs(t *testing.T) {
	args := buildDualOutputArgs("input.mp4", "out.wav", "out.m4a", []string{"-hwaccel", "videotoolbox"}, "aac_at")
	expected := []string{
		"-hide_banner", "-y",
		"-hwaccel", "videotoolbox",
		"-i", "input.mp4",
		"-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "out.wav",
		"-vn", "-c:a", "aac_at", "-b:a", "128k", "-movflags", "+faststart", "out.m4a",
	}

	if len(args) != len(expected) {
		t.Fatalf("args length mismatch: got %d, want %d", len(args), len(expected))
	}
	for i := range args {
		if args[i] != expected[i] {
			t.Errorf("arg[%d] = %q, want %q", i, args[i], expected[i])
		}
	}
}

func TestExtractSTTWavAndPlaybackAAC_InputValidation(t *testing.T) {
	ctx := context.Background()

	if _, err := ExtractSTTWavAndPlaybackAAC(ctx, "", "out.wav", "out.m4a"); err == nil {
		t.Errorf("expected error for empty rawInputPath")
	}
	if _, err := ExtractSTTWavAndPlaybackAAC(ctx, "input.wav", "", "out.m4a"); err == nil {
		t.Errorf("expected error for empty outWavPath")
	}
	if _, err := ExtractSTTWavAndPlaybackAAC(ctx, "input.wav", "out.wav", ""); err == nil {
		t.Errorf("expected error for empty outAACPath")
	}
	if _, err := ExtractSTTWavAndPlaybackAAC(ctx, "/path/does/not/exist/audio.wav", "out.wav", "out.m4a"); err == nil {
		t.Errorf("expected error for non-existent input file")
	}
}

func generateSyntheticAudio(t *testing.T, dst string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-y", "-f", "lavfi", "-i", "sine=frequency=1000:duration=1", dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to generate synthetic audio: %v | %s", err, string(out))
	}
}

func TestExtractSTTWavAndPlaybackAAC_Synthetic(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed, skipping test")
	}

	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "input_sine.wav")
	generateSyntheticAudio(t, inputPath)

	outWav := filepath.Join(tempDir, "output", "stt.wav")
	outAAC := filepath.Join(tempDir, "output", "playback.m4a")

	report, err := ExtractSTTWavAndPlaybackAAC(context.Background(), inputPath, outWav, outAAC)
	if err != nil {
		t.Fatalf("ExtractSTTWavAndPlaybackAAC failed: %v", err)
	}

	if report.ProfileUsed == "" {
		t.Errorf("expected ProfileUsed to be populated, got empty string")
	}
	if report.Elapsed <= 0 {
		t.Errorf("expected positive elapsed duration, got %v", report.Elapsed)
	}

	wavStat, err := os.Stat(outWav)
	if err != nil || wavStat.Size() == 0 {
		t.Fatalf("expected valid WAV output file, stat: %v", err)
	}

	aacStat, err := os.Stat(outAAC)
	if err != nil || aacStat.Size() == 0 {
		t.Fatalf("expected valid AAC output file, stat: %v", err)
	}
}

func TestExtractSTTWavAndPlaybackAAC_FallbackTrigger(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed, skipping test")
	}

	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "input_sine.wav")
	generateSyntheticAudio(t, inputPath)

	outWav := filepath.Join(tempDir, "fallback_stt.wav")
	outAAC := filepath.Join(tempDir, "fallback_playback.m4a")

	// Inject a profile with hardware acceleration enabled, but an invalid audio encoder
	// that causes the primary FFmpeg invocation to fail.
	failingProfile := HWProfile{
		Name:        ProfileVideotoolbox,
		HwaccelArgs: []string{"-hwaccel", "videotoolbox"},
		AACEncoder:  "nonexistent_audio_encoder_triggering_fallback",
	}

	report, err := ExtractSTTWavAndPlaybackAACWithProfile(context.Background(), inputPath, outWav, outAAC, failingProfile)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}

	if !report.FallbackUsed {
		t.Errorf("expected FallbackUsed to be true, got false")
	}
	if report.ProfileUsed != ProfileSoftwareFallback {
		t.Errorf("expected ProfileUsed to be %q, got %q", ProfileSoftwareFallback, report.ProfileUsed)
	}
	if report.Elapsed <= 0 {
		t.Errorf("expected positive elapsed time, got %v", report.Elapsed)
	}

	wavStat, err := os.Stat(outWav)
	if err != nil || wavStat.Size() == 0 {
		t.Fatalf("expected valid WAV output from fallback, stat: %v", err)
	}
	aacStat, err := os.Stat(outAAC)
	if err != nil || aacStat.Size() == 0 {
		t.Fatalf("expected valid AAC output from fallback, stat: %v", err)
	}
}

func TestExtractSTTWavAndPlaybackAAC_ContextCancelled(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed, skipping test")
	}

	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "input_sine.wav")
	generateSyntheticAudio(t, inputPath)

	outWav := filepath.Join(tempDir, "cancelled.wav")
	outAAC := filepath.Join(tempDir, "cancelled.m4a")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel context immediately

	_, err := ExtractSTTWavAndPlaybackAAC(ctx, inputPath, outWav, outAAC)
	if err == nil {
		t.Fatalf("expected error for cancelled context, got nil")
	}
}

func TestExtractSTTWavAndPlaybackAAC_CorruptInputBothFail(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed, skipping test")
	}

	tempDir := t.TempDir()
	corruptInput := filepath.Join(tempDir, "corrupt.mp4")
	if err := os.WriteFile(corruptInput, []byte("NOT_A_VALID_MEDIA_FILE_CONTENT"), 0644); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}

	outWav := filepath.Join(tempDir, "fail.wav")
	outAAC := filepath.Join(tempDir, "fail.m4a")

	failingProfile := HWProfile{
		Name:        ProfileVideotoolbox,
		HwaccelArgs: []string{"-hwaccel", "videotoolbox"},
		AACEncoder:  "aac",
	}

	report, err := ExtractSTTWavAndPlaybackAACWithProfile(context.Background(), corruptInput, outWav, outAAC, failingProfile)
	if err == nil {
		t.Fatalf("expected error for corrupt media input, got nil")
	}
	if !report.FallbackUsed {
		t.Errorf("expected fallback to be attempted when hwaccel profile failed on corrupt input")
	}
	if report.ProfileUsed != ProfileSoftwareFallback {
		t.Errorf("expected profile %q, got %q", ProfileSoftwareFallback, report.ProfileUsed)
	}
}
