package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
)

// TestStress_UploadService_TenantIsolationAndHijack verifies that:
// 1. An attacker cannot complete an upload initiated by another user.
// 2. Calling complete on an existing completed job cannot change its ownership or overwrite job records.
func TestStress_UploadService_TenantIsolationAndHijack(t *testing.T) {
	memStore := storage.NewMemoryStorage()
	ctx := context.Background()

	// In-memory job repository simulation
	jobs := map[string]*model.Job{}

	taskQueue := &mockTaskQueue{}

	s := NewUploadService(UploadServiceDeps{
		MaxUploadSizeMB: 50,
		DetectFileType:  func(string) string { return "audio" },
		AllowedFile:     func(name string) bool { return true },
		SortedExts:      func() []string { return []string{".mp3", ".wav"} },
		Storage:         memStore,
		TaskQueue:       taskQueue,
		SaveJobTx: func(tx model.Tx, id string, job *model.Job) error {
			if existing, exists := jobs[id]; exists {
				// Immutability rule: Never overwrite owner_id on conflict
				job.OwnerID = existing.OwnerID
			}
			jobs[id] = job
			return nil
		},
		JobFinder: func(id string) (*model.Job, error) {
			if j, ok := jobs[id]; ok {
				return j, nil
			}
			return nil, nil
		},
	})

	// Scenario 1: User "alice" initiates an upload
	initRes, err := s.InitiatePresigned(ctx, UploadInitiateRequest{
		OwnerID:     "alice",
		Filename:    "alice_confidential.mp3",
		Size:        1024 * 1024,
		ContentType: "audio/mpeg",
		Language:    "ko",
	})
	if err != nil {
		t.Fatalf("InitiatePresigned failed: %v", err)
	}
	jobID := initRes.JobID

	// Verify initial job state
	initialJob := jobs[jobID]
	if initialJob == nil || initialJob.OwnerID != "alice" {
		t.Fatalf("expected initial job owned by 'alice', got %+v", initialJob)
	}

	// Upload payload into quarantine
	_, err = memStore.Put(ctx, initRes.StorageKey, []byte("valid-audio-data"), "audio/mpeg")
	if err != nil {
		t.Fatalf("Put to quarantine failed: %v", err)
	}

	// Attack 1: User "mallory" attempts to complete Alice's upload
	_, err = s.CompletePresigned(ctx, UploadCompleteRequest{
		OwnerID:     "mallory",
		JobID:       jobID,
		Filename:    "mallory_hijacked.mp3",
		Description: "Hijacked by Mallory",
	})
	if err == nil {
		t.Fatal("expected error when Mallory attempts to complete Alice's upload, got nil")
	}
	httpErr, ok := err.(*HTTPError)
	if !ok || httpErr.Status != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for cross-tenant complete, got %v", err)
	}

	// Verify job ownership and filename were NOT hijacked
	jobAfterAttack := jobs[jobID]
	if jobAfterAttack.OwnerID != "alice" {
		t.Fatalf("CRITICAL SECURITY FLAW: job owner was changed to %q", jobAfterAttack.OwnerID)
	}
	if jobAfterAttack.Filename == "mallory_hijacked.mp3" {
		t.Fatalf("CRITICAL SECURITY FLAW: job filename was overwritten by attacker")
	}

	// Scenario 2: Attack on an existing COMPLETED job
	completedJobID := "job-alice-already-completed"
	originalCompletedJob := &model.Job{
		OwnerID:     "alice",
		Filename:    "alice_finished.mp3",
		FileType:    "audio",
		StatusCode:  model.JobStatusCompletedCode,
		Status:      model.JobStatusName(model.JobStatusCompletedCode),
		Description: "Original finished description",
		Tags:        []string{"confidential", "q3-earnings"},
		UploadedTS:  float64(time.Now().Unix()),
	}
	jobs[completedJobID] = originalCompletedJob

	// Attack 2A: Mallory calls complete without quarantine file
	_, err = s.CompletePresigned(ctx, UploadCompleteRequest{
		OwnerID:     "mallory",
		JobID:       completedJobID,
		Filename:    "mallory_overwrite.mp3",
		Description: "Mallory's overwrite",
	})
	if err == nil {
		t.Fatal("expected error completing completed job without quarantine file, got nil")
	}
	httpErr, ok = err.(*HTTPError)
	if !ok || httpErr.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request (missing quarantine object), got %v", err)
	}

	// Attack 2B: Mallory maliciously injects a quarantine file for the completed job
	fakeQuarantineKey := fmt.Sprintf("jobs/%s/quarantine_raw", completedJobID)
	_, _ = memStore.Put(ctx, fakeQuarantineKey, []byte("fake-audio"), "audio/mpeg")

	_, err = s.CompletePresigned(ctx, UploadCompleteRequest{
		OwnerID:     "mallory",
		JobID:       completedJobID,
		Filename:    "mallory_overwrite.mp3",
		Description: "Mallory's overwrite",
	})
	if err == nil {
		t.Fatal("expected 403 Forbidden when Mallory attempts to complete Alice's completed job, got nil")
	}
	httpErr, ok = err.(*HTTPError)
	if !ok || httpErr.Status != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", err)
	}

	// Verify original completed job is completely unmodified
	compJobAfter := jobs[completedJobID]
	if compJobAfter.OwnerID != "alice" {
		t.Fatalf("CRITICAL: completed job owner changed to %q", compJobAfter.OwnerID)
	}
	if compJobAfter.StatusCode != model.JobStatusCompletedCode {
		t.Fatalf("CRITICAL: completed job status code reverted from %d to %d", model.JobStatusCompletedCode, compJobAfter.StatusCode)
	}
	if compJobAfter.Description != "Original finished description" {
		t.Fatalf("CRITICAL: completed job description overwritten to %q", compJobAfter.Description)
	}

	// Legit completion: Alice completes her own upload
	completedJob, err := s.CompletePresigned(ctx, UploadCompleteRequest{
		OwnerID:     "alice",
		JobID:       jobID,
		Filename:    "alice_confidential.mp3",
		Description: "Alice's legit description",
		Tags:        []string{"legit"},
	})
	if err != nil {
		t.Fatalf("legit CompletePresigned failed: %v", err)
	}
	if completedJob.OwnerID != "alice" {
		t.Fatalf("expected owner 'alice', got %q", completedJob.OwnerID)
	}
	if completedJob.Description != "Alice's legit description" {
		t.Fatalf("expected legit description, got %q", completedJob.Description)
	}
}
