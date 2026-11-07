package service

import (
	"context"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
)

func TestUploadService_Create_AudioHappyPath(t *testing.T) {
	tmp := t.TempDir()

	var (
		addedJobID     string
		addedJob       *model.Job
		savedBlobKind  string
		savedBlobBytes []byte
		enqueuedID     string
		setFieldsCalls int
	)

	s := NewUploadService(UploadServiceDeps{
		DetectFileType: func(name string) string { return "audio" },
		AllowedFile:    func(string) bool { return true },
		SortedExts:     func() []string { return []string{".mp3"} },

		ListTagNamesByOwner:     func(string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },
		GetFolderByID:           func(string, string) (*model.Folder, error) { return &model.Folder{}, nil },
		TouchFolderAndAncestors: func(string, string) error { return nil },

		SaveUploadWithLimit: func(_ *multipart.FileHeader, dst string, _ int64, _ int64) (int64, error) {
			b := []byte("ID3\x03\x00\x00\x00\x00\x00\x00mp3-bytes")
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				return 0, err
			}
			return int64(len(b)), nil
		},
		ConvertToAac: func(_ string, dst string) error {
			return os.WriteFile(dst, []byte("aac-bytes"), 0o644)
		},
		GetMediaDuration: func(string) *int { v := 12; return &v },
		FormatSecondsPtr: func(*int) string { return "" },
		SaveJobBlob: func(_ string, kind string, b []byte) error {
			savedBlobKind = kind
			savedBlobBytes = append([]byte(nil), b...)
			return nil
		},

		BlobKindAudioAAC:    "aac",
		BlobKindPDFOriginal: "pdf",

		AddJob: func(id string, j *model.Job) {
			addedJobID = id
			addedJob = j
		},
		SetJobFields:      func(string, map[string]any) { setFieldsCalls++ },
		EnqueueTranscribe: func(id string) { enqueuedID = id },

		TmpFolder:           tmp,
		MaxUploadSizeMB:     10,
		UploadRateLimitKBPS: 1000,
		StatusPending:       "pending",
		StatusFailed:        "failed",

		Now:      func() time.Time { return time.Unix(0, 0) },
		NewJobID: func() string { return "job-1" },
		Spawn:    func(fn func()) { fn() },
	})

	fh := &multipart.FileHeader{
		Filename: "a.mp3",
		Header:   textproto.MIMEHeader{"Content-Type": []string{"audio/mpeg"}},
	}
	jobID, filename, err := s.Create(UploadCreateRequest{
		OwnerID:     "u1",
		DisplayName: "hello",
		FileHeader:  fh,
	})
	if err != nil {
		t.Fatalf("Create err=%v", err)
	}
	if jobID != "job-1" {
		t.Fatalf("jobID=%q", jobID)
	}
	if filename != "hello.mp3" {
		t.Fatalf("filename=%q", filename)
	}
	if addedJobID != "job-1" || addedJob == nil {
		t.Fatalf("AddJob not called")
	}
	if addedJob.FileType != "audio" {
		t.Fatalf("addedJob.FileType=%q", addedJob.FileType)
	}
	if savedBlobKind != "aac" || string(savedBlobBytes) != "aac-bytes" {
		t.Fatalf("saved blob kind=%q bytes=%q", savedBlobKind, string(savedBlobBytes))
	}
	if enqueuedID != "job-1" {
		t.Fatalf("enqueuedID=%q", enqueuedID)
	}
	if setFieldsCalls == 0 {
		t.Fatalf("expected SetJobFields to be called")
	}

	if _, err := os.Stat(filepath.Join(tmp, "job-1_temp.mp3")); !os.IsNotExist(err) {
		t.Fatalf("temp upload file should be removed; stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "job-1.m4a")); !os.IsNotExist(err) {
		t.Fatalf("aac temp file should be removed; stat err=%v", err)
	}
}

func TestUploadService_Create_PDFHappyPath(t *testing.T) {
	tmp := t.TempDir()

	var (
		savedBlobKind string
		enqueuedID    string
	)

	s := NewUploadService(UploadServiceDeps{
		DetectFileType: func(name string) string { return "pdf" },
		AllowedFile:    func(string) bool { return true },
		SortedExts:     func() []string { return []string{".pdf"} },

		ListTagNamesByOwner: func(string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },

		SaveUploadWithLimit: func(_ *multipart.FileHeader, dst string, _ int64, _ int64) (int64, error) {
			b := []byte("%PDF-1.4 mock content")
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				return 0, err
			}
			return int64(len(b)), nil
		},
		FormatSecondsPtr: func(*int) string { return "" },
		SaveJobBlob: func(_ string, kind string, _ []byte) error {
			savedBlobKind = kind
			return nil
		},

		BlobKindAudioAAC:    "aac",
		BlobKindPDFOriginal: "pdf",

		AddJob:            func(string, *model.Job) {},
		SetJobFields:      func(string, map[string]any) {},
		EnqueuePDFExtract: func(id string) { enqueuedID = id },

		TmpFolder:           tmp,
		MaxUploadSizeMB:     10,
		UploadRateLimitKBPS: 1000,
		StatusPending:       "pending",
		StatusFailed:        "failed",

		NewJobID: func() string { return "job-pdf" },
		Spawn:    func(fn func()) { fn() },
	})

	fh := &multipart.FileHeader{
		Filename: "x.pdf",
		Header:   textproto.MIMEHeader{"Content-Type": []string{"application/pdf"}},
	}
	jobID, filename, err := s.Create(UploadCreateRequest{
		OwnerID:    "u1",
		FileHeader: fh,
	})
	if err != nil {
		t.Fatalf("Create err=%v", err)
	}
	if jobID != "job-pdf" {
		t.Fatalf("jobID=%q", jobID)
	}
	if filename != "x.pdf" {
		t.Fatalf("filename=%q", filename)
	}
	if savedBlobKind != "pdf" {
		t.Fatalf("savedBlobKind=%q", savedBlobKind)
	}
	if enqueuedID != "job-pdf" {
		t.Fatalf("enqueuedID=%q", enqueuedID)
	}
}

func TestUploadService_Create_PathTraversalSanitization(t *testing.T) {
	tmp := t.TempDir()
	s := NewUploadService(UploadServiceDeps{
		AllowedFile: func(name string) bool {
			return strings.HasSuffix(name, ".pdf")
		},
		DetectFileType: func(string) string { return "pdf" },
		ListTagNamesByOwner: func(string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },
		SaveJobBlob:    func(string, string, []byte) error { return nil },
		SaveUploadWithLimit: func(_ *multipart.FileHeader, dst string, _ int64, _ int64) (int64, error) {
			b := []byte("%PDF-1.4 mock evil")
			_ = os.WriteFile(dst, b, 0o644)
			return int64(len(b)), nil
		},
		BlobKindPDFOriginal: "pdf",
		AddJob:              func(string, *model.Job) {},
		SetJobFields:        func(string, map[string]any) {},
		EnqueuePDFExtract:   func(string) {},
		TmpFolder:           tmp,
		MaxUploadSizeMB:     10,
		UploadRateLimitKBPS: 1000,
		StatusPending:       "pending",
		NewJobID:            func() string { return "job-traversal" },
		Spawn:               func(fn func()) { fn() },
	})

	fh := &multipart.FileHeader{
		Filename: "../../../etc/evil.pdf",
		Header:   textproto.MIMEHeader{"Content-Type": []string{"application/pdf"}},
	}
	_, filename, err := s.Create(UploadCreateRequest{
		OwnerID:    "u1",
		FileHeader: fh,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if filename != "evil.pdf" {
		t.Fatalf("expected sanitized filename 'evil.pdf', got %q", filename)
	}
}

func TestUploadService_Create_InvalidMagicBytes(t *testing.T) {
	tmp := t.TempDir()
	s := NewUploadService(UploadServiceDeps{
		AllowedFile:         func(name string) bool { return true },
		DetectFileType:      func(string) string { return "pdf" },
		ListTagNamesByOwner: func(string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },
		NewJobID:            func() string { return "job-invalid" },
		SaveUploadWithLimit: func(_ *multipart.FileHeader, dst string, _ int64, _ int64) (int64, error) {
			b := []byte("plain text disguised as pdf")
			_ = os.WriteFile(dst, b, 0o644)
			return int64(len(b)), nil
		},
		TmpFolder: tmp,
	})

	fh := &multipart.FileHeader{
		Filename: "fake.pdf",
		Header:   textproto.MIMEHeader{"Content-Type": []string{"application/pdf"}},
	}
	_, _, err := s.Create(UploadCreateRequest{
		OwnerID:    "u1",
		FileHeader: fh,
	})
	if err == nil {
		t.Fatal("expected error due to invalid magic bytes, got nil")
	}
	if !strings.Contains(err.Error(), "PDF") {
		t.Fatalf("expected PDF magic byte error message, got %v", err)
	}
}

func TestUploadService_FinalizeUploadedAudio_PanicRecovery(t *testing.T) {
	tmp := t.TempDir()
	tempPath := filepath.Join(tmp, "job-panic_temp.mp3")
	aacPath := filepath.Join(tmp, "job-panic.m4a")
	_ = os.WriteFile(tempPath, []byte("temp"), 0o644)
	_ = os.WriteFile(aacPath, []byte("aac"), 0o644)

	var failedFields map[string]any
	s := NewUploadService(UploadServiceDeps{
		StatusFailed: "failed",
		ConvertToAac: func(string, string) error {
			panic("simulated ffmpeg driver panic")
		},
		SaveJobBlob: func(string, string, []byte) error { return nil },
		SetJobFields: func(id string, fields map[string]any) {
			failedFields = fields
		},
	})

	s.finalizeUploadedAudio("job-panic", tempPath, aacPath)

	if failedFields == nil {
		t.Fatal("expected SetJobFields to be called on panic")
	}
	if failedFields["status_code"] != model.JobStatusAudioConvertFailedCode {
		t.Fatalf("expected status_code %d, got %v", model.JobStatusAudioConvertFailedCode, failedFields["status_code"])
	}

	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("expected tempPath to be removed on panic, got err=%v", err)
	}
	if _, err := os.Stat(aacPath); !os.IsNotExist(err) {
		t.Fatalf("expected aacPath to be removed on panic, got err=%v", err)
	}
}


type mockTaskQueue struct {
	enqueued []model.Task
}

func (m *mockTaskQueue) Enqueue(ctx context.Context, tx model.Tx, task model.Task) error {
	m.enqueued = append(m.enqueued, task)
	return nil
}

func (m *mockTaskQueue) Claim(ctx context.Context, workerID string, classes []model.ResourceClass, leaseDur time.Duration) (*model.Task, error) {
	return nil, nil
}

func (m *mockTaskQueue) Heartbeat(ctx context.Context, taskID, workerID string, leaseDur time.Duration) error {
	return nil
}

func (m *mockTaskQueue) Complete(ctx context.Context, tx model.Tx, taskID, workerID string, attempt int) error {
	return nil
}

func (m *mockTaskQueue) Fail(ctx context.Context, tx model.Tx, taskID, workerID string, attempt int, retryAfter *time.Duration, errMsg string) error {
	return nil
}

func TestUploadService_Create_DurableTaskQueue(t *testing.T) {
	tmp := t.TempDir()
	q := &mockTaskQueue{}

	s := NewUploadService(UploadServiceDeps{
		TmpFolder:           tmp,
		MaxUploadSizeMB:     10,
		UploadRateLimitKBPS: 0,
		DetectFileType:      func(string) string { return "audio" },
		AllowedFile:         func(string) bool { return true },
		GetMediaDuration:    func(string) *int { d := 10; return &d },
		FormatSecondsPtr:    func(*int) string { return "00:10" },
		ListTagNamesByOwner: func(string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },
		SaveUploadWithLimit: func(h *multipart.FileHeader, dst string, a, b int64) (int64, error) {
			// Write valid ID3 MP3 bytes
			mp3Bytes := []byte{'I', 'D', '3', 3, 0, 0, 0, 0, 0, 0}
			return int64(len(mp3Bytes)), os.WriteFile(dst, mp3Bytes, 0o644)
		},
		Now:           func() time.Time { return time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC) },
		NewJobID:      func() string { return "job_durable_1" },
		StatusPending: "pending",
		TaskQueue:     q,
	})

	hdr := &multipart.FileHeader{Filename: "audio.mp3", Size: 10}
	jobID, name, err := s.Create(UploadCreateRequest{
		OwnerID:    "user_123",
		FileHeader: hdr,
		Language:   "ko",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if jobID == "" || name != "audio.mp3" {
		t.Fatalf("unexpected jobID=%s name=%s", jobID, name)
	}

	if len(q.enqueued) != 1 {
		t.Fatalf("expected 1 task enqueued, got %d", len(q.enqueued))
	}
	enq := q.enqueued[0]
	if enq.JobID != jobID || enq.Step != model.StepAVScan || enq.ResourceClass != model.ResourceClassMedia {
		t.Fatalf("unexpected task: %+v", enq)
	}
}

func TestUploadService_InitiateAndCompletePresigned(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.NewLocalStorage(tmp, storage.WithSecretKey("test-secret-key"))
	if err != nil {
		t.Fatalf("NewLocalStorage failed: %v", err)
	}

	q := &mockTaskQueue{}
	var savedJob *model.Job
	var savedJobID string
	var outboxEvents []model.DomainEvent

	s := NewUploadService(UploadServiceDeps{
		MaxUploadSizeMB: 50,
		DetectFileType:  func(string) string { return "audio" },
		AllowedFile:     func(name string) bool { return strings.HasSuffix(name, ".mp3") || strings.HasSuffix(name, ".m4a") },
		SortedExts:      func() []string { return []string{".mp3", ".m4a"} },
		Storage:         store,
		TaskQueue:       q,
		SaveJobTx: func(tx model.Tx, id string, job *model.Job) error {
			savedJobID = id
			savedJob = job
			return nil
		},
		JobFinder: func(id string) (*model.Job, error) {
			if id == savedJobID && savedJob != nil {
				return savedJob, nil
			}
			return nil, nil
		},
		InsertOutboxEvent: func(ctx context.Context, tx model.Tx, evt model.DomainEvent) (int64, error) {
			outboxEvents = append(outboxEvents, evt)
			return int64(len(outboxEvents)), nil
		},
	})

	ctx := context.Background()

	// 1. Validation Failures on Initiate
	_, err = s.InitiatePresigned(ctx, UploadInitiateRequest{
		OwnerID:  "",
		Filename: "audio.mp3",
	})
	if err == nil {
		t.Fatal("expected error on empty ownerID")
	}

	_, err = s.InitiatePresigned(ctx, UploadInitiateRequest{
		OwnerID:  "u1",
		Filename: "malicious.exe",
	})
	if err == nil {
		t.Fatal("expected error on disallowed extension")
	}

	_, err = s.InitiatePresigned(ctx, UploadInitiateRequest{
		OwnerID:  "u1",
		Filename: "audio.mp3",
		Size:     100 * 1024 * 1024, // 100MB > 50MB
	})
	if err == nil {
		t.Fatal("expected error on oversized file")
	}

	// 2. Successful Initiate
	initRes, err := s.InitiatePresigned(ctx, UploadInitiateRequest{
		OwnerID:     "u1",
		Filename:    "test.mp3",
		Size:        5 * 1024 * 1024,
		ContentType: "audio/mpeg",
		Language:    "ko",
	})
	if err != nil {
		t.Fatalf("InitiatePresigned failed: %v", err)
	}
	if initRes.JobID == "" || initRes.UploadURL == "" || initRes.StorageKey == "" {
		t.Fatalf("invalid initiate response: %+v", initRes)
	}

	// 3. Complete before uploading to storage should fail
	_, err = s.CompletePresigned(ctx, UploadCompleteRequest{
		OwnerID:  "u1",
		JobID:    initRes.JobID,
		Filename: "test.mp3",
	})
	if err == nil {
		t.Fatal("expected error when quarantine object does not exist")
	}

	// 4. Simulate direct client PUT upload to quarantine_raw
	quarantineData := []byte("fake-mp3-quarantine-binary-data")
	_, err = store.Put(ctx, initRes.StorageKey, quarantineData, "audio/mpeg")
	if err != nil {
		t.Fatalf("direct put failed: %v", err)
	}

	// 5. Successful Complete
	job, err := s.CompletePresigned(ctx, UploadCompleteRequest{
		OwnerID:     "u1",
		JobID:       initRes.JobID,
		Filename:    "test.mp3",
		Language:    "ko",
		Tags:        []string{"회의", "오디오"},
		Description: "Presigned direct upload test",
	})
	if err != nil {
		t.Fatalf("CompletePresigned failed: %v", err)
	}
	if job.StatusCode != model.JobStatusPendingCode {
		t.Fatalf("expected pending status code %d, got %d", model.JobStatusPendingCode, job.StatusCode)
	}
	if savedJobID != initRes.JobID || savedJob == nil {
		t.Fatalf("job not saved via SaveJobTx: id=%s", savedJobID)
	}

	// 6. Verify Task enqueued for av_scan
	if len(q.enqueued) != 1 {
		t.Fatalf("expected 1 task enqueued, got %d", len(q.enqueued))
	}
	task := q.enqueued[0]
	if task.JobID != initRes.JobID || task.Step != model.StepAVScan || task.ResourceClass != model.ResourceClassMedia {
		t.Fatalf("unexpected task enqueued: %+v", task)
	}

	// 7. Verify Outbox event published
	if len(outboxEvents) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(outboxEvents))
	}
	if outboxEvents[0].JobID != initRes.JobID || outboxEvents[0].EventType != model.EventTypeJobStatus {
		t.Fatalf("unexpected outbox event: %+v", outboxEvents[0])
	}

	// 8. Adversarial Test: Cross-tenant user attempting CompletePresigned for another user's job must fail with 403 Forbidden
	_, crossTenantErr := s.CompletePresigned(ctx, UploadCompleteRequest{
		OwnerID:  "attacker-bob",
		JobID:    initRes.JobID,
		Filename: "hijacked.mp3",
	})
	if crossTenantErr == nil {
		t.Fatal("expected CompletePresigned to fail with 403 Forbidden for cross-tenant attacker, got nil")
	}
	he, ok := crossTenantErr.(*HTTPError)
	if !ok || he.Status != http.StatusForbidden {
		t.Fatalf("expected HTTP 403 Forbidden, got: %v", crossTenantErr)
	}

	// 9. Adversarial Test: CompletePresigned on non-existent job (with quarantined object) must fail with 403 Forbidden
	nonExistKey := "jobs/non-existent-job-id/quarantine_raw"
	_, _ = store.Put(ctx, nonExistKey, []byte("data"), "audio/mp3")
	_, nonExistentErr := s.CompletePresigned(ctx, UploadCompleteRequest{
		OwnerID:  "u1",
		JobID:    "non-existent-job-id",
		Filename: "test.mp3",
	})
	if nonExistentErr == nil {
		t.Fatal("expected CompletePresigned to fail with 403 Forbidden for non-existent job, got nil")
	}
	heNonExist, ok := nonExistentErr.(*HTTPError)
	if !ok || heNonExist.Status != http.StatusForbidden {
		t.Fatalf("expected HTTP 403 Forbidden, got: %v", nonExistentErr)
	}
}
