package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
	"whisperserver/src/internal/service"
)

// mockFailPutStreamStorage wraps a Storage but fails on PutStream for specific keys.
type mockFailPutStreamStorage struct {
	storage.Storage
	failPutStreamKeyPrefix string
	putStreamCallCount     int
	deletedKeys            []string
}

func (m *mockFailPutStreamStorage) PutStream(ctx context.Context, key string, r io.Reader, size int64, contentType string) (storage.ObjectInfo, error) {
	m.putStreamCallCount++
	if m.failPutStreamKeyPrefix != "" && strings.HasPrefix(key, m.failPutStreamKeyPrefix) {
		return storage.ObjectInfo{}, errors.New("simulated storage put stream failure")
	}
	return m.Storage.PutStream(ctx, key, r, size, contentType)
}

func (m *mockFailPutStreamStorage) Delete(ctx context.Context, key string) error {
	m.deletedKeys = append(m.deletedKeys, key)
	return m.Storage.Delete(ctx, key)
}

// TestAVScanStepHandler_RetainsQuarantineOnPromotionError verifies BUG-01:
// If PutStream fails when promoting quarantine_raw to source_raw, quarantine_raw MUST NOT be deleted.
func TestAVScanStepHandler_RetainsQuarantineOnPromotionError(t *testing.T) {
	tmp := t.TempDir()
	baseStore, err := storage.NewLocalStorage(tmp, storage.WithSecretKey("test-secret"))
	if err != nil {
		t.Fatalf("NewLocalStorage failed: %v", err)
	}

	ctx := context.Background()
	jobID := "job-quarantine-guard"
	quarantineKey := fmt.Sprintf("jobs/%s/quarantine_raw", jobID)
	sourceKey := fmt.Sprintf("jobs/%s/source_raw", jobID)

	// Put quarantine object
	quarantineData := []byte("audio-quarantine-media-payload")
	_, err = baseStore.Put(ctx, quarantineKey, quarantineData, "audio/mp3")
	if err != nil {
		t.Fatalf("put quarantine object failed: %v", err)
	}

	// Create failing storage that rejects putting into sourceKey
	failStore := &mockFailPutStreamStorage{
		Storage:                baseStore,
		failPutStreamKeyPrefix: sourceKey,
	}

	h := &AVScanStepHandler{
		Storage: failStore,
		Scanner: &mockScanner{
			verdict: domain.VerdictClean,
		},
	}

	task := &domain.Task{
		JobID:       jobID,
		Step:        domain.StepAVScan,
		PayloadJSON: fmt.Sprintf(`{"storage_key":%q,"file_type":"audio"}`, quarantineKey),
	}

	_, err = h.Execute(ctx, task)
	if err == nil {
		t.Fatal("expected Execute to return error on PutStream promotion failure, got nil")
	}
	if !strings.Contains(err.Error(), "promote quarantine to source_raw failed") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// CRITICAL INVARIANT: quarantineKey MUST still exist in storage!
	exists, err := baseStore.Exists(ctx, quarantineKey)
	if err != nil {
		t.Fatalf("Exists check failed: %v", err)
	}
	if !exists {
		t.Fatalf("VIOLATION: quarantine object %q was deleted despite promotion error!", quarantineKey)
	}

	// Verify Delete was never called for quarantineKey
	for _, k := range failStore.deletedKeys {
		if k == quarantineKey {
			t.Fatalf("VIOLATION: Delete(%q) was called!", quarantineKey)
		}
	}
}

// TestNormalizeStepHandler_DeferCleanupAndNoWavSQLiteDualWrite verifies R-12:
// 1. Temporary files on disk are removed via defer cleanup.
// 2. When Storage is active, uncompressed WAV is not dual-written to BlobSvc.
func TestNormalizeStepHandler_DeferCleanupAndNoWavSQLiteDualWrite(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := storage.NewLocalStorage(filepath.Join(tmpDir, "storage"), storage.WithSecretKey("test-secret"))
	if err != nil {
		t.Fatalf("NewLocalStorage failed: %v", err)
	}

	ctx := context.Background()
	jobID := "job-normalize-r12"
	sourceKey := fmt.Sprintf("jobs/%s/source_raw", jobID)

	// Synthesize a valid minimal WAV file for dual conversion test
	wavHeader := []byte{
		'R', 'I', 'F', 'F',
		36, 0, 0, 0, // file size - 8
		'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ',
		16, 0, 0, 0, // subchunk1 size (16 for PCM)
		1, 0, // audio format (1 = PCM)
		1, 0, // num channels (1)
		0x80, 0x3e, 0, 0, // sample rate (16000)
		0x00, 0x7d, 0, 0, // byte rate (16000 * 2)
		2, 0, // block align (2)
		16, 0, // bits per sample (16)
		'd', 'a', 't', 'a',
		0, 0, 0, 0, // subchunk2 size (0 data bytes)
	}
	_, err = store.Put(ctx, sourceKey, wavHeader, "audio/wav")
	if err != nil {
		t.Fatalf("store source_raw failed: %v", err)
	}

	var savedBlobs = map[string][]byte{}
	blobSvc := service.NewJobBlobService(service.JobBlobServiceDeps{
		SaveJobBlob: func(id, kind string, b []byte) error {
			savedBlobs[kind] = b
			return nil
		},
		BlobKindAudioAAC: "audio_aac",
	})

	h := &NormalizeStepHandler{
		Storage:   store,
		BlobSvc:   blobSvc,
		TmpFolder: tmpDir,
	}

	task := &domain.Task{
		JobID:       jobID,
		Step:        domain.StepNormalize,
		PayloadJSON: fmt.Sprintf(`{"language":"ko"}`),
	}

	// Paths that NormalizeStepHandler defines
	rawInputPath := filepath.Join(tmpDir, jobID+"_source_raw")
	outWavPath := filepath.Join(tmpDir, jobID+"_temp_stt.wav")
	outAACPath := filepath.Join(tmpDir, jobID+"_playback.m4a")

	res, err := h.Execute(ctx, task)
	if err != nil {
		t.Fatalf("NormalizeStepHandler.Execute failed: %v", err)
	}
	if res.NextStep == nil || *res.NextStep != domain.StepTranscribe {
		t.Fatalf("expected next step transcribe, got: %+v", res)
	}

	// 1. Verify defer cleanup removed all temporary files on disk
	if _, err := os.Stat(rawInputPath); !os.IsNotExist(err) {
		t.Errorf("expected rawInputPath %q to be removed, but it exists", rawInputPath)
	}
	if _, err := os.Stat(outWavPath); !os.IsNotExist(err) {
		t.Errorf("expected outWavPath %q to be removed, but it exists", outWavPath)
	}
	if _, err := os.Stat(outAACPath); !os.IsNotExist(err) {
		t.Errorf("expected outAACPath %q to be removed, but it exists", outAACPath)
	}

	// 2. Verify temporary STT WAV was NOT written into BlobSvc (no SQLite dual-write)
	if _, hasWav := savedBlobs["wav"]; hasWav {
		t.Errorf("expected WAV SQLite dual-write to be skipped when Storage != nil, but wav blob was saved")
	}

	// 3. Verify temporary STT WAV is present in Storage for TranscribeStepHandler
	tempWavKey := fmt.Sprintf("jobs/%s/temp_stt.wav", jobID)
	hasWavStorage, err := store.Exists(ctx, tempWavKey)
	if err != nil || !hasWavStorage {
		t.Errorf("expected %q to exist in Storage for transcribe step", tempWavKey)
	}
}
