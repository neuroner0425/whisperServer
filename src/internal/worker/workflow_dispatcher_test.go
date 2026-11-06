package worker

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/gemini"
	intwhisper "whisperserver/src/internal/integrations/whisper"
	"whisperserver/src/internal/queue"
	"whisperserver/src/internal/repo/sqlite"
	"whisperserver/src/internal/service"
)

type mockScanner struct {
	verdict   domain.ScanVerdict
	signature string
}

func (m *mockScanner) ScanStream(ctx context.Context, r io.Reader, size int64) (domain.ScanResult, error) {
	return domain.ScanResult{
		Verdict:   m.verdict,
		Signature: m.signature,
		EngineVer: "mock-clamd",
	}, nil
}

func (m *mockScanner) Ping(ctx context.Context) error {
	return nil
}

type mockWhisperRunner struct{}

func (m *mockWhisperRunner) RunFromBlobWithOptions(ctx context.Context, jobID string, wavBytes []byte, totalSec *int, opts intwhisper.RunOptions) (intwhisper.RunResult, error) {
	return intwhisper.RunResult{
		TimelineText:   "[00:00:00,000 --> 00:00:02,000] 테스트 전사 결과입니다.",
		TranscriptJSON: []byte(`{"segments":[]}`),
	}, nil
}

func setupDispatcherTestDB(t *testing.T) {
	t.Helper()
	projectRoot := t.TempDir()
	if err := sqlite.Init(projectRoot); err != nil {
		t.Fatalf("sqlite.Init failed: %v", err)
	}
	t.Cleanup(func() {
		sqlite.Close()
	})
}

func createJobForDispatcher(t *testing.T, jobID, ownerID string, refineEnabled int) {
	t.Helper()
	_ = sqlite.GetRawTx(nil) // ensure package init
	// insert user & job directly
	rawDB := getDB(t)
	_, err := rawDB.Exec(`INSERT INTO users (id, email, password_hash) VALUES (?, ?, 'hash')`, ownerID, ownerID+"@example.com")
	if err != nil {
		t.Fatalf("insert user failed: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO jobs (id, owner_id, status_code, filename, refine_enabled) VALUES (?, ?, 10, 'audio.mp3', ?)`, jobID, ownerID, refineEnabled)
	if err != nil {
		t.Fatalf("insert job failed: %v", err)
	}
}

func getDB(t *testing.T) *sql.DB {
	t.Helper()
	db := sqlite.GetRawDBForTest()
	if db == nil {
		t.Fatalf("sqlite.GetRawDBForTest returned nil")
	}
	return db
}

func TestWorkflowDispatcher_PipelineSuccess(t *testing.T) {
	setupDispatcherTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jobID := "job_pipe_1"
	ownerID := "user_pipe_1"
	createJobForDispatcher(t, jobID, ownerID, 1) // refine enabled

	// Create dummy upload file
	tmpDir := t.TempDir()
	tempPath := filepath.Join(tmpDir, "upload.mp3")
	if err := os.WriteFile(tempPath, []byte("fake mp3 binary content"), 0o644); err != nil {
		t.Fatalf("write temp upload file failed: %v", err)
	}

	taskQ := queue.NewSqliteQueue()
	txMgr := sqlite.NewTxManager(nil)

	blobSvc := service.NewJobBlobService(service.JobBlobServiceDeps{
		LoadJobBlob: sqlite.LoadJobBlob,
		SaveJobBlob: sqlite.SaveJobBlob,
	})

	dispatcher := NewWorkflowDispatcher("test_worker_1", taskQ, txMgr, nil, t.Logf, func(scope string, err error, f string, args ...any) {
		t.Logf("ERR [%s] %s: %s", scope, fmt.Sprintf(f, args...), err)
	})

	// Register Step Handlers
	scanner := &mockScanner{verdict: domain.VerdictClean}
	dispatcher.RegisterStepHandler(domain.StepAVScan, &AVScanStepHandler{Scanner: scanner})
	dispatcher.RegisterStepHandler(domain.StepNormalize, &NormalizeStepHandler{
		BlobSvc:      blobSvc,
		ConvertToAac: func(src, dst string) error { return os.WriteFile(dst, []byte("fake aac"), 0o644) },
		ConvertToWav: func(src, dst string) error { return os.WriteFile(dst, []byte("fake wav"), 0o644) },
	})
	dispatcher.RegisterStepHandler(domain.StepTranscribe, &TranscribeStepHandler{
		BlobSvc:       blobSvc,
		WhisperRunner: &mockWhisperRunner{},
	})
	dispatcher.RegisterStepHandler(domain.StepPolish, &PolishStepHandler{
		BlobSvc: blobSvc,
		PolishTranscriptTimeline: func(ctx context.Context, jobID, transcript string, cfg gemini.LLMStageConfig) (string, error) {
			return "[00:00:00,000 --> 00:00:02,000] 교정된 텍스트입니다.", nil
		},
	})
	dispatcher.RegisterStepHandler(domain.StepStructure, &StructureStepHandler{
		BlobSvc: blobSvc,
		StructureTranscriptParagraphs: func(ctx context.Context, jobID, timeline string, cfg gemini.LLMStageConfig) (string, error) {
			return `{"paragraphs":[{"text":"구조화된 문단"}]}`, nil
		},
	})

	// Initial Enqueue: av_scan
	initialTask := domain.Task{
		ID:            "task_init",
		JobID:         jobID,
		OwnerID:       ownerID,
		Step:          domain.StepAVScan,
		ResourceClass: domain.ResourceClassMedia,
		PayloadJSON:   fmt.Sprintf(`{"temp_path":%q,"file_type":"mp3","language":"ko","refine_enabled":true}`, tempPath),
	}
	if err := taskQ.Enqueue(ctx, nil, initialTask); err != nil {
		t.Fatalf("enqueue initial task failed: %v", err)
	}

	// Start dispatcher
	dispatcher.Start(ctx)
	defer dispatcher.Stop()

	// Wait for pipeline to reach completed state (status_code = 50)
	rawDB := getDB(t)
	var statusCode int
	for i := 0; i < 40; i++ {
		time.Sleep(100 * time.Millisecond)
		err := rawDB.QueryRow(`SELECT status_code FROM jobs WHERE id = ?`, jobID).Scan(&statusCode)
		if err == nil && statusCode == 50 {
			break
		}
	}

	if statusCode != 50 {
		t.Fatalf("expected job status_code 50, got %d", statusCode)
	}

	// Verify job_steps contains all completed steps
	steps, err := sqlite.ListJobSteps(ctx, ownerID, jobID)
	if err != nil {
		t.Fatalf("ListJobSteps failed: %v", err)
	}
	if len(steps) != 5 {
		t.Fatalf("expected 5 steps (av_scan, normalize, transcribe, polish, structure), got %d: %+v", len(steps), steps)
	}

	expectedSteps := []domain.StepType{
		domain.StepAVScan,
		domain.StepNormalize,
		domain.StepTranscribe,
		domain.StepPolish,
		domain.StepStructure,
	}
	for i, exp := range expectedSteps {
		if steps[i].Step != exp || steps[i].Status != domain.StepStatusCompleted {
			t.Fatalf("step %d: expected %s completed, got %+v", i, exp, steps[i])
		}
	}

	// Verify outbox has index.requested event
	unpub, err := sqlite.FetchUnpublishedOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("FetchUnpublishedOutbox failed: %v", err)
	}
	hasIndexRequested := false
	for _, ev := range unpub {
		if ev.EventType == domain.EventTypeIndexRequested && ev.JobID == jobID {
			hasIndexRequested = true
			break
		}
	}
	if !hasIndexRequested {
		t.Fatalf("expected index.requested outbox event for job %s, got: %+v", jobID, unpub)
	}
}

func TestWorkflowDispatcher_InfectedMalwareBlocked(t *testing.T) {
	setupDispatcherTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jobID := "job_malware_1"
	ownerID := "user_malware_1"
	createJobForDispatcher(t, jobID, ownerID, 0)

	// Create dummy infected file
	tmpDir := t.TempDir()
	tempPath := filepath.Join(tmpDir, "infected.mp3")
	if err := os.WriteFile(tempPath, []byte("EICAR-STANDARD-ANTIVIRUS-TEST-FILE"), 0o644); err != nil {
		t.Fatalf("write infected file failed: %v", err)
	}

	taskQ := queue.NewSqliteQueue()
	txMgr := sqlite.NewTxManager(nil)

	dispatcher := NewWorkflowDispatcher("test_worker_2", taskQ, txMgr, nil, t.Logf, func(scope string, err error, f string, args ...any) {
		t.Logf("ERR [%s] %s: %s", scope, fmt.Sprintf(f, args...), err)
	})

	// Scanner reports infected
	scanner := &mockScanner{verdict: domain.VerdictInfected, signature: "Win.Test.EICAR_HDB-1"}
	dispatcher.RegisterStepHandler(domain.StepAVScan, &AVScanStepHandler{Scanner: scanner})

	initialTask := domain.Task{
		ID:            "task_malware",
		JobID:         jobID,
		OwnerID:       ownerID,
		Step:          domain.StepAVScan,
		ResourceClass: domain.ResourceClassMedia,
		PayloadJSON:   fmt.Sprintf(`{"temp_path":%q,"file_type":"mp3"}`, tempPath),
	}
	if err := taskQ.Enqueue(ctx, nil, initialTask); err != nil {
		t.Fatalf("enqueue task failed: %v", err)
	}

	dispatcher.Start(ctx)
	defer dispatcher.Stop()

	// Wait for infected status
	rawDB := getDB(t)
	var statusCode int
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		err := rawDB.QueryRow(`SELECT status_code FROM jobs WHERE id = ?`, jobID).Scan(&statusCode)
		if err == nil && statusCode == domain.JobStatusQuarantinedInfectedCode {
			break
		}
	}

	if statusCode != domain.JobStatusQuarantinedInfectedCode {
		t.Fatalf("expected status_code %d, got %d", domain.JobStatusQuarantinedInfectedCode, statusCode)
	}

	// Verify infected file was deleted from disk
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("expected infected file to be removed, but it still exists")
	}

	// Verify task is dead (no retry)
	taskRecord, err := sqlite.GetTask(ctx, "task_malware")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if taskRecord.Status != domain.TaskStatusDead {
		t.Fatalf("expected dead task status, got %s", taskRecord.Status)
	}
}
