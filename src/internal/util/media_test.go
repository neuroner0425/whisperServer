package util

import (
	"encoding/binary"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDetectPhaseCancellationSyntheticStereo(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	normalPath := filepath.Join(dir, "normal.wav")
	invertedPath := filepath.Join(dir, "inverted.wav")
	writeSyntheticStereo(t, normalPath, false)
	writeSyntheticStereo(t, invertedPath, true)

	normal, err := DetectPhaseCancellation(normalPath)
	if err != nil {
		t.Fatalf("detect normal: %v", err)
	}
	if normal.ShouldUseChannelMono {
		t.Fatalf("normal stereo should not use channel mono: %+v", normal)
	}
	if normal.GlobalCorrelation < 0.9 {
		t.Fatalf("expected positive correlation for normal stereo, got %+v", normal)
	}

	inverted, err := DetectPhaseCancellation(invertedPath)
	if err != nil {
		t.Fatalf("detect inverted: %v", err)
	}
	if !inverted.ShouldUseChannelMono {
		t.Fatalf("inverted stereo should use channel mono: %+v", inverted)
	}
	if inverted.GlobalCorrelation > -0.9 {
		t.Fatalf("expected negative correlation for inverted stereo, got %+v", inverted)
	}
	if inverted.FlaggedRatio < 0.9 {
		t.Fatalf("expected most active windows to be flagged, got %+v", inverted)
	}
}

func TestConvertToWavAvoidsPhaseCancellation(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "inverted.wav")
	normalDownmix := filepath.Join(dir, "normal-downmix.wav")
	adaptiveDownmix := filepath.Join(dir, "adaptive-downmix.wav")
	writeSyntheticStereo(t, src, true)

	if err := convertToWavUsingNormalDownmix(src, normalDownmix); err != nil {
		t.Fatalf("normal downmix: %v", err)
	}
	if err := ConvertToWav(src, adaptiveDownmix); err != nil {
		t.Fatalf("adaptive downmix: %v", err)
	}

	normalRMS := readMonoRMS(t, normalDownmix)
	adaptiveRMS := readMonoRMS(t, adaptiveDownmix)
	if normalRMS > 0.001 {
		t.Fatalf("expected normal downmix to be nearly silent, rms=%f", normalRMS)
	}
	if adaptiveRMS < 0.01 {
		t.Fatalf("expected adaptive downmix to preserve audio, rms=%f", adaptiveRMS)
	}
	if adaptiveRMS < normalRMS*20 {
		t.Fatalf("expected adaptive downmix to be much louder, normal=%f adaptive=%f", normalRMS, adaptiveRMS)
	}
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
}

func writeSyntheticStereo(t *testing.T, path string, invertRight bool) {
	t.Helper()
	rightFilter := "[r]"
	filter := "[0:a]asplit=2[l][r];"
	if invertRight {
		filter += "[r]volume=-1[rneg];"
		rightFilter = "[rneg]"
	}
	filter += "[l]" + rightFilter + "amerge=inputs=2[a]"
	cmd := exec.Command(
		"ffmpeg",
		"-hide_banner",
		"-nostats",
		"-v", "error",
		"-y",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration=5:sample_rate=48000",
		"-filter_complex", filter,
		"-map", "[a]",
		"-ac", "2",
		"-c:a", "pcm_s16le",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write synthetic stereo: %v | output=%s", err, out)
	}
}

func readMonoRMS(t *testing.T, path string) float64 {
	t.Helper()
	cmd := exec.Command(
		"ffmpeg",
		"-hide_banner",
		"-nostats",
		"-v", "error",
		"-i", path,
		"-f", "f32le",
		"-acodec", "pcm_f32le",
		"-ac", "1",
		"-ar", "16000",
		"-",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read mono rms: %v", err)
	}
	if len(out) < 4 {
		t.Fatalf("no decoded samples")
	}
	var sum float64
	var n int
	for off := 0; off+3 < len(out); off += 4 {
		v := float64(math.Float32frombits(binary.LittleEndian.Uint32(out[off : off+4])))
		sum += v * v
		n++
	}
	return math.Sqrt(sum / float64(n))
}
