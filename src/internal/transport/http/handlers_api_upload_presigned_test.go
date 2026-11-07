package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
	"whisperserver/src/internal/service"
)

func TestPresignedUploadHandlers(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.NewLocalStorage(tmp, storage.WithSecretKey("secret-test-key"))
	if err != nil {
		t.Fatalf("NewLocalStorage failed: %v", err)
	}

	var savedJob *model.Job
	var enqueuedTasks []model.Task

	uploadSvc := service.NewUploadService(service.UploadServiceDeps{
		MaxUploadSizeMB: 20,
		DetectFileType:  func(string) string { return "audio" },
		AllowedFile:     func(string) bool { return true },
		Storage:         store,
		TaskQueue: &mockTaskQueue{
			enqueueFn: func(ctx context.Context, tx model.Tx, task model.Task) error {
				enqueuedTasks = append(enqueuedTasks, task)
				return nil
			},
		},
		SaveJobTx: func(tx model.Tx, id string, job *model.Job) error {
			savedJob = job
			return nil
		},
		JobFinder: func(id string) (*model.Job, error) {
			return savedJob, nil
		},
	})

	h := PresignedUploadHandlers{
		CurrentUserOrUnauthorized: func(c echo.Context) (*User, bool) {
			return &User{ID: "user-1", LoginID: "user1"}, true
		},
		UploadSvc: uploadSvc,
	}

	e := echo.New()

	// 1. POST /api/uploads/initiate
	initBody := map[string]any{
		"filename":     "lecture.mp4",
		"size":         1024 * 1024,
		"content_type": "audio/mp4",
		"language":     "ko",
	}
	bodyBytes, _ := json.Marshal(initBody)
	req := httptest.NewRequest(http.MethodPost, "/api/uploads/initiate", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = h.Initiate()(c)
	if err != nil {
		t.Fatalf("Initiate() error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var initRes map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &initRes)
	jobID, _ := initRes["job_id"].(string)
	storageKey, _ := initRes["storage_key"].(string)
	uploadURL, _ := initRes["upload_url"].(string)

	if jobID == "" || storageKey == "" || uploadURL == "" {
		t.Fatalf("invalid initiate response: %+v", initRes)
	}

	// 2. Client simulates direct PUT upload to quarantine_raw via storage
	_, err = store.Put(context.Background(), storageKey, []byte("lecture-audio-data"), "audio/mp4")
	if err != nil {
		t.Fatalf("store.Put failed: %v", err)
	}

	// 3. POST /api/uploads/:job_id/complete
	completeBody := map[string]any{
		"filename":    "lecture.mp4",
		"language":    "ko",
		"tags":        []string{"강의", "수업"},
		"description": "lecture recording",
	}
	bodyBytes, _ = json.Marshal(completeBody)
	req = httptest.NewRequest(http.MethodPost, "/api/uploads/"+jobID+"/complete", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	c.SetParamNames("job_id")
	c.SetParamValues(jobID)

	err = h.Complete()(c)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var compRes map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &compRes)
	if compRes["job_id"] != jobID || compRes["status_code"] != float64(model.JobStatusPendingCode) {
		t.Fatalf("unexpected complete response: %+v", compRes)
	}

	if savedJob == nil || savedJob.Filename != "lecture.mp4" {
		t.Fatalf("saved job mismatch: %+v", savedJob)
	}

	if len(enqueuedTasks) != 1 || enqueuedTasks[0].JobID != jobID || enqueuedTasks[0].Step != model.StepAVScan {
		t.Fatalf("enqueued task mismatch: %+v", enqueuedTasks)
	}

	// 4. Adversarial Attack: Cross-tenant attacker Bob tries to complete Alice's job
	hAttacker := PresignedUploadHandlers{
		CurrentUserOrUnauthorized: func(c echo.Context) (*User, bool) {
			return &User{ID: "attacker-bob", LoginID: "bob"}, true
		},
		UploadSvc: uploadSvc,
	}
	reqAttack := httptest.NewRequest(http.MethodPost, "/api/uploads/"+jobID+"/complete", bytes.NewReader(bodyBytes))
	reqAttack.Header.Set("Content-Type", "application/json")
	recAttack := httptest.NewRecorder()
	cAttack := e.NewContext(reqAttack, recAttack)
	cAttack.SetParamNames("job_id")
	cAttack.SetParamValues(jobID)

	err = hAttacker.Complete()(cAttack)
	if err == nil {
		t.Fatal("expected 403 Forbidden for cross-tenant attacker, got nil err")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected echo.HTTPError 403, got: %v", err)
	}
}

type mockTaskQueue struct {
	enqueueFn func(ctx context.Context, tx model.Tx, task model.Task) error
}

func (m *mockTaskQueue) Enqueue(ctx context.Context, tx model.Tx, task model.Task) error {
	if m.enqueueFn != nil {
		return m.enqueueFn(ctx, tx, task)
	}
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
