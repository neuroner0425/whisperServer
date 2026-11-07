package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
)

// failingStorage wraps MemoryStorage but forces PutStream on destination keys to fail
type failingStorage struct {
	*storage.MemoryStorage
	failPutStreamKeyPrefix string
	putStreamError         error
	deletedKeys            []string
}

type mockMalwareScanner struct {
	verdict   domain.ScanVerdict
	signature string
	engine    string
}

func (m *mockMalwareScanner) Ping(ctx context.Context) error {
	return nil
}

func (m *mockMalwareScanner) ScanStream(ctx context.Context, r io.Reader, size int64) (domain.ScanResult, error) {
	return domain.ScanResult{
		Verdict:   m.verdict,
		Signature: m.signature,
		EngineVer: m.engine,
	}, nil
}

func (f *failingStorage) PutStream(ctx context.Context, key string, r io.Reader, size int64, contentType string) (storage.ObjectInfo, error) {
	if f.failPutStreamKeyPrefix != "" && strings.HasPrefix(key, f.failPutStreamKeyPrefix) {
		return storage.ObjectInfo{}, f.putStreamError
	}
	return f.MemoryStorage.PutStream(ctx, key, r, size, contentType)
}

func (f *failingStorage) Delete(ctx context.Context, key string) error {
	f.deletedKeys = append(f.deletedKeys, key)
	return f.MemoryStorage.Delete(ctx, key)
}

// TestStress_AVScan_PromotionError_PreservesQuarantine verifies that if PutStream
// fails during promotion of a clean file from quarantine_raw to source_raw,
// the quarantine file is preserved and NOT prematurely deleted.
func TestStress_AVScan_PromotionError_PreservesQuarantine(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemoryStorage()

	jobID := "job-stress-quarantine-1"
	quarantineKey := fmt.Sprintf("jobs/%s/quarantine_raw", jobID)
	quarantinePayload := []byte("clean-customer-audio-content-12345")

	_, err := mem.Put(ctx, quarantineKey, quarantinePayload, "audio/mp4")
	if err != nil {
		t.Fatalf("mem.Put failed: %v", err)
	}

	simulatedErr := errors.New("storage backend S3: 507 Insufficient Storage / disk quota exceeded")
	storeWrapper := &failingStorage{
		MemoryStorage:          mem,
		failPutStreamKeyPrefix: fmt.Sprintf("jobs/%s/source_raw", jobID),
		putStreamError:         simulatedErr,
	}

	cleanScanner := &mockMalwareScanner{
		verdict: domain.VerdictClean,
		engine:  "ClamAV 1.0-StressTest",
	}

	handler := &AVScanStepHandler{
		Scanner: cleanScanner,
		Storage: storeWrapper,
	}

	taskPayload, _ := json.Marshal(map[string]any{
		"storage_key": quarantineKey,
		"file_type":   "audio",
		"language":    "ko",
	})
	task := &domain.Task{
		ID:          "task-av-stress-1",
		JobID:       jobID,
		Step:        domain.StepAVScan,
		PayloadJSON: string(taskPayload),
	}

	// Execute handler
	stepRes, execErr := handler.Execute(ctx, task)
	if execErr == nil {
		t.Fatalf("expected AVScanStepHandler to return error when PutStream fails, got nil (stepRes: %+v)", stepRes)
	}
	if !strings.Contains(execErr.Error(), "promote quarantine to source_raw failed") {
		t.Fatalf("expected promotion failure error, got: %v", execErr)
	}

	// CRITICAL ASSERTION: The quarantine file MUST NOT be deleted!
	qExists, err := mem.Exists(ctx, quarantineKey)
	if err != nil {
		t.Fatalf("mem.Exists failed: %v", err)
	}
	if !qExists {
		t.Fatalf("CRITICAL BUG: quarantine file %q was prematurely deleted despite promotion PutStream failure!", quarantineKey)
	}

	// Verify quarantine content was not corrupted
	qData, err := mem.Get(ctx, quarantineKey)
	if err != nil || !bytes.Equal(qData, quarantinePayload) {
		t.Fatalf("quarantine data was corrupted or unreadable after failed promotion: %v", err)
	}

	// Verify Delete was never called for quarantineKey
	for _, delKey := range storeWrapper.deletedKeys {
		if delKey == quarantineKey {
			t.Fatalf("CRITICAL BUG: Delete was invoked for quarantine key %q upon PutStream failure", quarantineKey)
		}
	}
}

// TestStress_AVScan_InfectedFile_ImmediatelyDeletesQuarantine verifies that when malware
// is detected, the quarantine file is immediately and permanently deleted.
func TestStress_AVScan_InfectedFile_ImmediatelyDeletesQuarantine(t *testing.T) {
	ctx := context.Background()
	mem := storage.NewMemoryStorage()

	jobID := "job-stress-infected-1"
	quarantineKey := fmt.Sprintf("jobs/%s/quarantine_raw", jobID)
	infectedPayload := []byte("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*")

	_, err := mem.Put(ctx, quarantineKey, infectedPayload, "audio/mp4")
	if err != nil {
		t.Fatalf("mem.Put failed: %v", err)
	}

	infectedScanner := &mockMalwareScanner{
		verdict:   domain.VerdictInfected,
		signature: "Eicar-Test-Signature",
		engine:    "ClamAV 1.0-StressTest",
	}

	handler := &AVScanStepHandler{
		Scanner: infectedScanner,
		Storage: mem,
	}

	taskPayload, _ := json.Marshal(map[string]any{
		"storage_key": quarantineKey,
		"file_type":   "audio",
		"language":    "ko",
	})
	task := &domain.Task{
		ID:          "task-av-stress-infected",
		JobID:       jobID,
		Step:        domain.StepAVScan,
		PayloadJSON: string(taskPayload),
	}

	_, execErr := handler.Execute(ctx, task)
	if execErr == nil {
		t.Fatal("expected error on infected scan, got nil")
	}

	// Quarantine file must be deleted immediately
	qExists, _ := mem.Exists(ctx, quarantineKey)
	if qExists {
		t.Fatalf("CRITICAL DEFECT: infected file %q was NOT deleted from quarantine storage!", quarantineKey)
	}

	// Source file must NOT exist
	sourceKey := fmt.Sprintf("jobs/%s/source_raw", jobID)
	sExists, _ := mem.Exists(ctx, sourceKey)
	if sExists {
		t.Fatalf("CRITICAL DEFECT: infected file was promoted to sourceKey %q!", sourceKey)
	}
}
