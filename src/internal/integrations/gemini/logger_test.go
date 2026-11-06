package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whisperserver/src/internal/worker"
)

func TestRecordAPILog(t *testing.T) {
	tempDir := t.TempDir()
	SetAPILogDir(tempDir)
	defer SetAPILogDir("logs/api")

	entry := APILogEntry{
		Timestamp:    time.Now().Format(time.RFC3339),
		CallType:     "refine",
		APIKeySuffix: "1234",
		Model:        "gemini-2.5-flash",
		DurationMs:   120,
		Request: APIRequestLog{
			SystemPrompt: "Test System Prompt",
			UserPrompt:   "Test User Prompt",
			HasSchema:    true,
		},
		Response: APIResponseLog{
			RawText:      `{"status":"ok"}`,
			FinishReason: "STOP",
			TotalTokens:  50,
		},
		FailureCriteriaResult: FailureCriteriaResult{
			Failed: false,
		},
	}

	if err := RecordAPILog(entry); err != nil {
		t.Fatalf("failed to record api log: %v", err)
	}

	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("failed to read temp log dir: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(files))
	}

	content, err := os.ReadFile(filepath.Join(tempDir, files[0].Name()))
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	var readEntry APILogEntry
	if err := json.Unmarshal(content, &readEntry); err != nil {
		t.Fatalf("failed to unmarshal log file json: %v", err)
	}

	if readEntry.CallType != "refine" || readEntry.Request.SystemPrompt != "Test System Prompt" {
		t.Errorf("unexpected content in logged file: %+v", readEntry)
	}
}

func TestBuildChunkImagesMeta(t *testing.T) {
	chunk := worker.DocumentChunk{
		ChunkIndex: 1,
		Images: []worker.DocumentPageImage{
			{PageIndex: 1, MIMEType: "image/jpeg", Data: []byte("12345")},
			{PageIndex: 2, MIMEType: "image/jpeg", Data: []byte("abcdefgh")},
		},
	}
	meta := buildChunkImagesMeta(chunk)
	if len(meta) != 2 {
		t.Fatalf("expected 2 image meta entries, got %d", len(meta))
	}
	if meta[0].PageIndex != 1 || meta[0].SizeBytes != 5 {
		t.Errorf("unexpected meta 0: %+v", meta[0])
	}
	if meta[1].PageIndex != 2 || meta[1].SizeBytes != 8 {
		t.Errorf("unexpected meta 1: %+v", meta[1])
	}
}

