package whisper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildTimelineTranscriptText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	raw := `{"transcription":[{"timestamps":{"from":"00:00:01","to":"00:00:02"},"offsets":{"from":0,"to":1},"text":" hello "}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := buildTimelineTranscriptText(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != `00:00:01 : "hello"` {
		t.Fatalf("unexpected timeline text: %q", got)
	}
}

func TestBuildSlimTranscriptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	raw := `{"transcription":[{"timestamps":{"from":"00:00:01","to":"00:00:02"},"offsets":{"from":0,"to":1},"text":" hello "}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := buildSlimTranscriptJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatal(err)
	}
	text := parsed["segments"].([]any)[0].(map[string]any)["text"].(string)
	if text != "hello" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestSplitOnCRLF(t *testing.T) {
	advance, token, err := splitOnCRLF([]byte("a\r\nb"), false)
	if err != nil || advance == 0 || strings.TrimSpace(string(token)) != "a" {
		t.Fatalf("unexpected split result: advance=%d token=%q err=%v", advance, token, err)
	}
}

func TestNormalizeLivePreviewTimelineLine(t *testing.T) {
	got, ok := normalizeLivePreviewTimelineLine(`[00:00:01.230 --> 00:00:03.450] 안녕하세요`)
	if !ok {
		t.Fatalf("expected live timeline line to parse")
	}
	want := "00:00:01,230 --> 00:00:03,450 안녕하세요"
	if got != want {
		t.Fatalf("normalizeLivePreviewTimelineLine() = %q, want %q", got, want)
	}
}

func TestVADFallbackReasonDetectsLargeTimelineGap(t *testing.T) {
	segments := []transcriptSegment{
		{Timestamps: transcriptTimestamps{From: "00:00:00,000", To: "00:00:05,000"}},
		{Timestamps: transcriptTimestamps{From: "00:00:36,001", To: "00:00:40,000"}},
	}
	if got := vadFallbackReason(segments); !strings.Contains(got, "timeline_gap") {
		t.Fatalf("expected timeline_gap fallback reason, got %q", got)
	}
}

func TestVADFallbackReasonDetectsLongSegment(t *testing.T) {
	segments := []transcriptSegment{
		{Timestamps: transcriptTimestamps{From: "00:00:00,000", To: "00:01:01,001"}},
	}
	if got := vadFallbackReason(segments); !strings.Contains(got, "segment_duration") {
		t.Fatalf("expected segment_duration fallback reason, got %q", got)
	}
}

func TestVADFallbackReasonAllowsNormalTimeline(t *testing.T) {
	segments := []transcriptSegment{
		{Timestamps: transcriptTimestamps{From: "00:00:00,000", To: "00:00:10,000"}},
		{Timestamps: transcriptTimestamps{From: "00:00:30,000", To: "00:00:45,000"}},
	}
	if got := vadFallbackReason(segments); got != "" {
		t.Fatalf("expected no fallback reason, got %q", got)
	}
}

func TestNeedsVADFallbackTranscriptJSONSkipsFallbackSource(t *testing.T) {
	raw := []byte(`{"source":"vad_off_fallback","segments":[{"from":"00:00:00,000","to":"00:00:05,000","text":"a"},{"from":"00:01:00,000","to":"00:01:05,000","text":"b"}]}`)
	got, err := NeedsVADFallbackTranscriptJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected fallback source to be skipped, got %q", got)
	}
}
