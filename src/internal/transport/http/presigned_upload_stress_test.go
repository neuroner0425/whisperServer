package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
	"whisperserver/src/internal/service"
)

// TestStress_HTTP_PresignedUpload_TenantIsolationAndCompletedJobProtection verifies:
// 1. Attacker calling POST /api/uploads/:job_id/complete for victim's job gets 403 Forbidden.
// 2. Attacker calling complete on victim's already completed job gets 403 Forbidden, and records are not overwritten.
func TestStress_HTTP_PresignedUpload_TenantIsolationAndCompletedJobProtection(t *testing.T) {
	memStore := storage.NewMemoryStorage()
	jobs := map[string]*model.Job{}

	uploadSvc := service.NewUploadService(service.UploadServiceDeps{
		MaxUploadSizeMB: 50,
		DetectFileType:  func(string) string { return "audio" },
		AllowedFile:     func(string) bool { return true },
		Storage:         memStore,
		SaveJobTx: func(tx model.Tx, id string, job *model.Job) error {
			if existing, exists := jobs[id]; exists {
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

	// Variable to simulate currently authenticated user in Echo session
	var currentSessionUser *User

	handlers := PresignedUploadHandlers{
		CurrentUserOrUnauthorized: func(c echo.Context) (*User, bool) {
			if currentSessionUser == nil {
				return nil, false
			}
			return currentSessionUser, true
		},
		UploadSvc: uploadSvc,
	}

	e := echo.New()

	// 1. Alice initiates upload
	currentSessionUser = &User{ID: "alice", LoginID: "alice"}
	initBody := `{"filename":"alice_speech.mp3","size":1000,"content_type":"audio/mpeg","language":"ko"}`
	reqInit := httptest.NewRequest(http.MethodPost, "/api/uploads/initiate", bytes.NewReader([]byte(initBody)))
	reqInit.Header.Set("Content-Type", "application/json")
	recInit := httptest.NewRecorder()
	cInit := e.NewContext(reqInit, recInit)

	err := handlers.Initiate()(cInit)
	if err != nil {
		t.Fatalf("Alice Initiate failed: %v", err)
	}
	if recInit.Code != http.StatusOK {
		t.Fatalf("Alice Initiate returned status %d, body: %s", recInit.Code, recInit.Body.String())
	}

	var initResp service.UploadInitiateResponse
	if err := json.Unmarshal(recInit.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("Unmarshal initiate response failed: %v", err)
	}
	jobID := initResp.JobID

	// Put audio payload in storage quarantine
	_, err = memStore.Put(context.Background(), initResp.StorageKey, []byte("sample audio content"), "audio/mpeg")
	if err != nil {
		t.Fatalf("storage put failed: %v", err)
	}

	// 2. Mallory attempts to complete Alice's job
	currentSessionUser = &User{ID: "mallory", LoginID: "mallory"}
	completeBody := `{"filename":"mallory_stolen.mp3","description":"Stolen by mallory","tags":["hacked"]}`
	reqComplete := httptest.NewRequest(http.MethodPost, "/api/uploads/"+jobID+"/complete", bytes.NewReader([]byte(completeBody)))
	reqComplete.Header.Set("Content-Type", "application/json")
	recComplete := httptest.NewRecorder()
	cComplete := e.NewContext(reqComplete, recComplete)
	cComplete.SetParamNames("job_id")
	cComplete.SetParamValues(jobID)

	err = handlers.Complete()(cComplete)
	if err == nil {
		t.Fatalf("expected error when Mallory attempts to complete Alice's job, got nil")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", err)
	}

	// Verify Alice's job record was not mutated
	job := jobs[jobID]
	if job == nil || job.OwnerID != "alice" {
		t.Fatalf("CRITICAL SECURITY DEFECT: job ownership was compromised! %+v", job)
	}
	if job.Filename == "mallory_stolen.mp3" {
		t.Fatalf("CRITICAL SECURITY DEFECT: job filename was overwritten by attacker")
	}

	// 3. Setup an existing COMPLETED job for Alice
	completedJobID := "job-alice-finished-123"
	jobs[completedJobID] = &model.Job{
		OwnerID:     "alice",
		Filename:    "alice_final.mp3",
		FileType:    "audio",
		StatusCode:  model.JobStatusCompletedCode,
		Status:      model.JobStatusName(model.JobStatusCompletedCode),
		Description: "Alice's finished transcript",
		Tags:        []string{"confidential"},
		UploadedTS:  float64(time.Now().Unix()),
	}

	// Inject quarantine object for this completed job to simulate adversary payload injection
	fakeKey := fmt.Sprintf("jobs/%s/quarantine_raw", completedJobID)
	_, _ = memStore.Put(context.Background(), fakeKey, []byte("adversary payload"), "audio/mpeg")

	// Mallory attempts to complete Alice's completed job
	currentSessionUser = &User{ID: "mallory", LoginID: "mallory"}
	reqCompMallory := httptest.NewRequest(http.MethodPost, "/api/uploads/"+completedJobID+"/complete", bytes.NewReader([]byte(completeBody)))
	reqCompMallory.Header.Set("Content-Type", "application/json")
	recCompMallory := httptest.NewRecorder()
	cCompMallory := e.NewContext(reqCompMallory, recCompMallory)
	cCompMallory.SetParamNames("job_id")
	cCompMallory.SetParamValues(completedJobID)

	err = handlers.Complete()(cCompMallory)
	if err == nil {
		t.Fatalf("expected error when Mallory calls complete on completed job, got nil")
	}
	he, ok = err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", err)
	}

	// Verify completed job remains completely intact
	compJob := jobs[completedJobID]
	if compJob.OwnerID != "alice" || compJob.StatusCode != model.JobStatusCompletedCode || compJob.Description != "Alice's finished transcript" {
		t.Fatalf("CRITICAL: completed job record was compromised! %+v", compJob)
	}
}
