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
