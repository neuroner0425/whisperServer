package server

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
	store "whisperserver/src/internal/repo/sqlite"
	intruntime "whisperserver/src/internal/runtime"
	"whisperserver/src/internal/service"
)

func TestBootstrap_LifecycleAndDurableWiringE2E(t *testing.T) {
	tempDir := t.TempDir()
	origProjectRoot := projectRoot
	origTmpFolder := tmpFolder
	origToolInfo := pdfToolPDFInfo
	origToolPPM := pdfToolPDFToPPM
	origStaticDir := staticDir
	origMaxUpload := maxUploadSizeMB
	t.Cleanup(func() {
		projectRoot = origProjectRoot
		tmpFolder = origTmpFolder
		pdfToolPDFInfo = origToolInfo
		pdfToolPDFToPPM = origToolPPM
		staticDir = origStaticDir
		maxUploadSizeMB = origMaxUpload
	})

	maxUploadSizeMB = 100

	projectRoot = tempDir
	tmpFolder = filepath.Join(tempDir, "tmp")
	staticDir = filepath.Join(tempDir, "static")
	_ = os.MkdirAll(tmpFolder, 0o755)
	_ = os.MkdirAll(staticDir, 0o755)
	_ = os.MkdirAll(filepath.Join(tempDir, ".run"), 0o755)

	// Mock pdf tools
	infoPath := filepath.Join(tempDir, "pdfinfo")
	ppmPath := filepath.Join(tempDir, "pdftoppm")
	_ = os.WriteFile(infoPath, []byte("#!/bin/sh\necho 'Pages: 2'\nexit 0\n"), 0o755)
	_ = os.WriteFile(ppmPath, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	pdfToolPDFInfo = infoPath
	pdfToolPDFToPPM = ppmPath

	if err := store.Init(tempDir); err != nil {
		t.Fatalf("store.Init failed: %v", err)
	}

	rawDB := store.GetRawDBForTest()
	if _, err := rawDB.Exec("INSERT OR IGNORE INTO users (id, login_id, email, password_hash) VALUES ('owner-test-e2e', 'testuser', 'owner@example.com', 'hash')"); err != nil {
		t.Fatalf("insert user failed: %v", err)
	}

	memStorage := storage.NewMemoryStorage()
	store.SetBlobStorage(memStorage, "memory")

	appRuntime = intruntime.New(intruntime.Config{
		TmpFolder: tmpFolder,
		Now:       time.Now,
		LoadJobs:  store.LoadActiveJobs,
	})

	// 1. Verify appServices wiring (D-01 regression prevention)
	svc := newAppServices()
	if svc.dispatcher == nil {
		t.Fatal("expected dispatcher to be wired in appServices, got nil")
	}
	if svc.outboxRelay == nil {
		t.Fatal("expected outboxRelay to be wired in appServices, got nil")
	}
	if svc.uploadSvc == nil {
		t.Fatal("expected uploadSvc to be wired, got nil")
	}

	// 2. Simulate Upload via UploadService
	pdfContent := []byte("%PDF-1.4 mock pdf document content for e2e test")
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="test.pdf"`)
	h.Set("Content-Type", "application/pdf")
	part, err := writer.CreatePart(h)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	_, _ = part.Write(pdfContent)
	_ = writer.Close()

	reader := multipart.NewReader(body, writer.Boundary())
	form, err := reader.ReadForm(1024 * 1024)
	if err != nil {
		t.Fatalf("read multipart form: %v", err)
	}
	fileHeader := form.File["file"][0]

	jobID, displayName, err := svc.uploadSvc.Create(service.UploadCreateRequest{
		OwnerID:    "owner-test-e2e",
		Language:   "ko",
		FileHeader: fileHeader,
	})
	if err != nil {
		t.Fatalf("uploadSvc.Create failed: %v", err)
	}
	if jobID == "" || displayName != "test.pdf" {
		t.Fatalf("unexpected job result: jobID=%s displayName=%s", jobID, displayName)
	}

	// 3. Verify durable task enqueue in tasks table
	var taskCount int
	var step string
	db := store.GetRawDBForTest()
	row := db.QueryRowContext(context.Background(), "SELECT COUNT(*), step FROM tasks WHERE job_id = ? GROUP BY step", jobID)
	if err := row.Scan(&taskCount, &step); err != nil {
		t.Fatalf("query tasks table failed: %v", err)
	}
	if taskCount != 1 || step != string(domain.StepAVScan) {
		t.Fatalf("expected 1 task with step=av_scan, got count=%d step=%s", taskCount, step)
	}

	// 4. Start dispatcher and verify lifecycle graceful start and stop
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.dispatcher.Start(ctx)
	svc.outboxRelay.Start(ctx)

	// Short wait to ensure worker loop starts without panic
	time.Sleep(50 * time.Millisecond)

	svc.dispatcher.Stop()
	svc.outboxRelay.Stop()

	// 5. Phase 2: Presigned Direct Upload & Media URL E2E verification
	initRes, err := svc.uploadSvc.InitiatePresigned(context.Background(), service.UploadInitiateRequest{
		OwnerID:     "owner-test-e2e",
		Filename:    "presigned_lecture.mp3",
		Size:        1024 * 512,
		ContentType: "audio/mpeg",
		Language:    "ko",
	})
	if err != nil {
		t.Fatalf("InitiatePresigned failed: %v", err)
	}
	if initRes.JobID == "" || initRes.UploadURL == "" || initRes.StorageKey == "" {
		t.Fatalf("unexpected InitiatePresigned result: %+v", initRes)
	}

	// Direct PUT upload to storage
	audioData := []byte("audio-mp3-binary-data-for-presigned-test")
	_, err = store.GetBlobStorage().Put(context.Background(), initRes.StorageKey, audioData, "audio/mpeg")
	if err != nil {
		t.Fatalf("direct storage.Put failed: %v", err)
	}

	// Complete presigned upload
	completedJob, err := svc.uploadSvc.CompletePresigned(context.Background(), service.UploadCompleteRequest{
		OwnerID:     "owner-test-e2e",
		JobID:       initRes.JobID,
		Filename:    "presigned_lecture.mp3",
		Language:    "ko",
		Tags:        []string{"강의", "E2E"},
		Description: "Presigned E2E Test",
	})
	if err != nil {
		t.Fatalf("CompletePresigned failed: %v", err)
	}
	if completedJob.StatusCode != domain.JobStatusPendingCode {
		t.Fatalf("expected pending status code %d, got %d", domain.JobStatusPendingCode, completedJob.StatusCode)
	}

	// Verify task enqueued for presigned job
	var presignedTaskCount int
	row = db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM tasks WHERE job_id = ? AND step = 'av_scan'", initRes.JobID)
	if err := row.Scan(&presignedTaskCount); err != nil {
		t.Fatalf("query tasks for presigned job failed: %v", err)
	}
	if presignedTaskCount != 1 {
		t.Fatalf("expected 1 av_scan task for presigned job, got %d", presignedTaskCount)
	}

	// Verify PresignGet media URL generation
	mediaURL, err := store.GetBlobStorage().PresignGet(context.Background(), initRes.StorageKey, "download_lecture.mp3", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet failed: %v", err)
	}
	if mediaURL == "" {
		t.Fatal("expected non-empty presigned mediaURL")
	}
}

// TestBootstrap_PresignedUploadAndMediaURL_E2E resolves GAP-01 by explicitly providing
// the designated E2E test target name for presigned upload and media URL lifecycle.
func TestBootstrap_PresignedUploadAndMediaURL_E2E(t *testing.T) {
	TestBootstrap_LifecycleAndDurableWiringE2E(t)
}
