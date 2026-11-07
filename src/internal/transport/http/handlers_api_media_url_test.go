package httptransport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
)

func TestMediaURLHandlers_Get(t *testing.T) {
	memStore := storage.NewMemoryStorage()
	_, _ = memStore.Put(nil, "jobs/job-ok/audio_aac", []byte("audio-aac-data"), "audio/mp4")
	_, _ = memStore.Put(nil, "jobs/job-infected/audio_aac", []byte("virus"), "audio/mp4")

	jobs := map[string]*model.Job{
		"job-ok": {
			OwnerID:    "user-1",
			Filename:   "voice.m4a",
			FileType:   "audio",
			StatusCode: model.JobStatusCompletedCode,
		},
		"job-other-owner": {
			OwnerID:    "user-2",
			Filename:   "secret.m4a",
			FileType:   "audio",
			StatusCode: model.JobStatusCompletedCode,
		},
		"job-infected": {
			OwnerID:    "user-1",
			Filename:   "bad.m4a",
			FileType:   "audio",
			StatusCode: model.JobStatusQuarantinedInfectedCode,
		},
	}

	h := MediaURLHandlers{
		CurrentUserOrUnauthorized: func(c echo.Context) (*User, bool) {
			return &User{ID: "user-1", LoginID: "user1"}, true
		},
		GetJob: func(jobID string) *model.Job {
			return jobs[jobID]
		},
		Storage: memStore,
	}

	e := echo.New()

	// 1. Success case
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-ok/media-url", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("job_id")
	c.SetParamValues("job-ok")

	err := h.Get()(c)
	if err != nil {
		t.Fatalf("Get() returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var res map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["job_id"] != "job-ok" || res["media_url"] == "" || res["kind"] != "audio" {
		t.Fatalf("unexpected response payload: %+v", res)
	}

	// 2. Forbidden: Other owner (R-18)
	req = httptest.NewRequest(http.MethodGet, "/api/jobs/job-other-owner/media-url", nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	c.SetParamNames("job_id")
	c.SetParamValues("job-other-owner")

	err = h.Get()(c)
	if err == nil {
		t.Fatal("expected error for other owner job")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", err)
	}

	// 3. Forbidden: Quarantined Infected malware job
	req = httptest.NewRequest(http.MethodGet, "/api/jobs/job-infected/media-url", nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	c.SetParamNames("job_id")
	c.SetParamValues("job-infected")

	err = h.Get()(c)
	if err == nil {
		t.Fatal("expected error for quarantined job")
	}
	he, ok = err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", err)
	}
}
