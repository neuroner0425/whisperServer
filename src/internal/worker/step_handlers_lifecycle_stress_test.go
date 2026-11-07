package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
	intwhisper "whisperserver/src/internal/integrations/whisper"
	"whisperserver/src/internal/service"
)

// TestStress_Normalize_IntermediateFileCleanupOnConversionFailure verifies that
// when dual media conversion fails (e.g. corrupt or invalid audio payload),
// all intermediate disk files (rawInputPath, outWavPath, outAACPath) are cleaned up by defer.
func TestStress_Normalize_IntermediateFileCleanupOnConversionFailure(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	tmpDir := t.TempDir()

	jobID := "job-stress-corrupt-audio-1"
	sourceKey := fmt.Sprintf("jobs/%s/source_raw", jobID)

	// Provide garbage/corrupt payload that cannot be parsed by FFmpeg
	corruptPayload := []byte("INVALID_GARBAGE_HEADER_NOT_A_VALID_MEDIA_FILE_1234567890")
	_, err := store.Put(ctx, sourceKey, corruptPayload, "application/octet-stream")
	if err != nil {
		t.Fatalf("store.Put failed: %v", err)
	}

	h := &NormalizeStepHandler{
		Storage:   store,
		TmpFolder: tmpDir,
	}

	task := &domain.Task{
		ID:          "task-norm-stress-1",
		JobID:       jobID,
		Step:        domain.StepNormalize,
		PayloadJSON: `{"language":"ko"}`,
	}

	rawInputPath := filepath.Join(tmpDir, jobID+"_source_raw")
	outWavPath := filepath.Join(tmpDir, jobID+"_temp_stt.wav")
	outAACPath := filepath.Join(tmpDir, jobID+"_playback.m4a")

	_, execErr := h.Execute(ctx, task)
	if execErr == nil {
		t.Fatalf("expected NormalizeStepHandler to fail on corrupt audio input, got nil")
	}

	// Verify all 3 intermediate files were cleaned up by defer
	if _, err := os.Stat(rawInputPath); !os.IsNotExist(err) {
		t.Fatalf("LEAK: rawInputPath %q still exists after conversion failure!", rawInputPath)
	}
	if _, err := os.Stat(outWavPath); !os.IsNotExist(err) {
		t.Fatalf("LEAK: outWavPath %q still exists after conversion failure!", outWavPath)
	}
	if _, err := os.Stat(outAACPath); !os.IsNotExist(err) {
		t.Fatalf("LEAK: outAACPath %q still exists after conversion failure!", outAACPath)
	}
}

// TestStress_Normalize_IntermediateFileCleanupOnPanic verifies that
// even if a panic occurs during execution, defer os.Remove ensures disk cleanup.
func TestStress_Normalize_IntermediateFileCleanupOnPanic(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	tmpDir := t.TempDir()

	jobID := "job-stress-panic-1"
	sourceKey := fmt.Sprintf("jobs/%s/source_raw", jobID)

	_, err := store.Put(ctx, sourceKey, []byte("some-media-data"), "application/octet-stream")
	if err != nil {
		t.Fatalf("store.Put failed: %v", err)
	}

	// Provide a GetMediaDuration hook that panics
	h := &NormalizeStepHandler{
		Storage:   store,
		TmpFolder: tmpDir,
		GetMediaDuration: func(path string) *int {
			panic("simulated fatal hardware probe panic")
		},
	}

	task := &domain.Task{
		ID:          "task-norm-stress-panic",
		JobID:       jobID,
		Step:        domain.StepNormalize,
		PayloadJSON: `{"language":"ko"}`,
	}

	rawInputPath := filepath.Join(tmpDir, jobID+"_source_raw")
	outWavPath := filepath.Join(tmpDir, jobID+"_temp_stt.wav")
	outAACPath := filepath.Join(tmpDir, jobID+"_playback.m4a")

	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("expected panic to be recovered")
			}
		}()
		_, _ = h.Execute(ctx, task)
	}()

	// Defer in Execute should have removed the raw file that was written before panic
	if _, err := os.Stat(rawInputPath); !os.IsNotExist(err) {
		t.Fatalf("LEAK: rawInputPath %q still exists after panic!", rawInputPath)
	}
	if _, err := os.Stat(outWavPath); !os.IsNotExist(err) {
		t.Fatalf("LEAK: outWavPath %q still exists after panic!", outWavPath)
	}
	if _, err := os.Stat(outAACPath); !os.IsNotExist(err) {
		t.Fatalf("LEAK: outAACPath %q still exists after panic!", outAACPath)
	}
}

// TestStress_Normalize_NoWavSQLiteDualWrite verifies that uncompressed WAV is
// NEVER written to SQLite job_blobs when object storage is active.
func TestStress_Normalize_NoWavSQLiteDualWrite(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	tmpDir := t.TempDir()

	jobID := "job-stress-no-dual-write"
	sourceKey := fmt.Sprintf("jobs/%s/source_raw", jobID)

	// Valid minimal WAV header
	wavHeader := []byte{
		'R', 'I', 'F', 'F',
		36, 0, 0, 0,
		'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ',
		16, 0, 0, 0,
		1, 0,
		1, 0,
		0x80, 0x3e, 0, 0,
		0x00, 0x7d, 0, 0,
		2, 0,
		16, 0,
		'd', 'a', 't', 'a',
		0, 0, 0, 0,
	}
	_, err := store.Put(ctx, sourceKey, wavHeader, "audio/wav")
	if err != nil {
		t.Fatalf("store.Put failed: %v", err)
	}

	savedBlobs := map[string][]byte{}
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
		ID:          "task-norm-stress-blobs",
		JobID:       jobID,
		Step:        domain.StepNormalize,
		PayloadJSON: `{"language":"ko"}`,
	}

	res, err := h.Execute(ctx, task)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if res.NextStep == nil || *res.NextStep != domain.StepTranscribe {
		t.Fatalf("expected next step transcribe, got: %+v", res)
	}

	// Assert: WAV was NOT written to SQLite job_blobs
	if _, hasWav := savedBlobs["wav"]; hasWav {
		t.Fatalf("CRITICAL ARCHITECTURAL DEFECT (ADR-011): uncompressed WAV was written to SQLite job_blobs while Storage is active!")
	}

	// Assert: Temporary STT WAV was uploaded to object storage
	tempWavKey := fmt.Sprintf("jobs/%s/temp_stt.wav", jobID)
	hasWavStorage, err := store.Exists(ctx, tempWavKey)
	if err != nil || !hasWavStorage {
		t.Fatalf("expected %q in object storage, but missing", tempWavKey)
	}

	// Now run TranscribeStepHandler to verify lifecycle: temp_stt.wav MUST be permanently deleted after STT
	mockWhisper := &mockWhisperRunnerForLifecycle{
		runFn: func(ctx context.Context, jID string, audio []byte, dur *int, opts intwhisper.RunOptions) (intwhisper.RunResult, error) {
			return intwhisper.RunResult{
				TimelineText:   "[00:00:00.000 --> 00:00:01.000] Transcribed text",
				TranscriptJSON: []byte(`{"text":"Transcribed text"}`),
			}, nil
		},
	}

	transcribeHandler := &TranscribeStepHandler{
		Storage:       store,
		WhisperRunner: mockWhisper,
		GetJob: func(id string) *domain.Job {
			return &domain.Job{Filename: "test.wav"}
		},
	}

	transcribeTask := &domain.Task{
		ID:          "task-transcribe-stress",
		JobID:       jobID,
		Step:        domain.StepTranscribe,
		PayloadJSON: `{"language":"ko"}`,
	}

	_, err = transcribeHandler.Execute(ctx, transcribeTask)
	if err != nil {
		t.Fatalf("TranscribeStepHandler failed: %v", err)
	}

	// Assert: temp_stt.wav MUST be deleted from storage
	wavLingering, err := store.Exists(ctx, tempWavKey)
	if err != nil {
		t.Fatalf("store.Exists failed: %v", err)
	}
	if wavLingering {
		t.Fatalf("CRITICAL LIFECYCLE LEAK (R-12): temporary WAV %q was NOT deleted after transcription completion!", tempWavKey)
	}
}

type mockWhisperRunnerForLifecycle struct {
	runFn func(context.Context, string, []byte, *int, intwhisper.RunOptions) (intwhisper.RunResult, error)
}

func (m *mockWhisperRunnerForLifecycle) RunFromBlobWithOptions(ctx context.Context, jobID string, audio []byte, dur *int, opts intwhisper.RunOptions) (intwhisper.RunResult, error) {
	if m.runFn != nil {
		return m.runFn(ctx, jobID, audio, dur, opts)
	}
	return intwhisper.RunResult{}, nil
}
