package httptransport

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

func TestBatchDownloadStreamingSuccess(t *testing.T) {
	job1 := &model.Job{OwnerID: "user-1", Filename: "meeting.m4a", Status: "완료"}
	job2 := &model.Job{OwnerID: "user-1", Filename: "document.pdf", FileType: "pdf", Status: "완료"}

	blobSvc := service.NewJobBlobService(service.JobBlobServiceDeps{
		HasJobBlob: func(jobID, kind string) bool { return false },
		LoadJobJSON: func(jobID, kind string) (string, error) {
			if jobID == "job-1" {
				return `{"transcript":"회의 내용입니다."}`, nil
			}
			if jobID == "job-2" {
				return `{"pages":[{"page_number":1,"markdown":"# PDF 본문"}]}`, nil
			}
			return "", errors.New("not found")
		},
	})

	h := LegacyJobsHandlers{
		CurrentUserOrUnauthorized: func(echo.Context) (*User, bool) { return &User{ID: "user-1"}, true },
		GetJob: func(id string) *model.Job {
			if id == "job-1" {
				return job1
			}
			if id == "job-2" {
				return job2
			}
			return nil
		},
		StatusCompleted: "완료",
		BlobSvc:         blobSvc,
	}

	e := echo.New()
	form := url.Values{}
	form.Add("job_ids", "job-1")
	form.Add("job_ids", "job-2")

	req := httptest.NewRequest(http.MethodPost, "/batch-download", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.BatchDownload()(c); err != nil {
		t.Fatalf("batch download handler failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("expected application/zip, got %q", rec.Header().Get("Content-Type"))
	}

	// Verify valid zip integrity
	zipBytes := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("invalid zip archive output: %v", err)
	}

	entries := make(map[string]string)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("failed to open zip entry %s: %v", f.Name, err)
		}
		content, _ := io.ReadAll(rc)
		rc.Close()
		entries[f.Name] = string(content)
	}

	if _, ok := entries["meeting.md"]; !ok {
		t.Fatalf("missing meeting.md entry in zip, found: %v", entries)
	}
	if _, ok := entries["document.md"]; !ok {
		t.Fatalf("missing document.md entry in zip, found: %v", entries)
	}
}

func TestBatchDownloadMaxLimitExceeded(t *testing.T) {
	h := LegacyJobsHandlers{
		CurrentUserOrUnauthorized: func(echo.Context) (*User, bool) { return &User{ID: "user-1"}, true },
		GetJob: func(id string) *model.Job {
			return &model.Job{OwnerID: "user-1", Filename: id + ".m4a", Status: "완료"}
		},
		StatusCompleted: "완료",
		BlobSvc:         service.NewJobBlobService(service.JobBlobServiceDeps{}),
	}

	e := echo.New()
	form := url.Values{}
	for i := 0; i <= MaxBatchDownloadJobs; i++ {
		form.Add("job_ids", "job-"+strconv.Itoa(i))
	}

	req := httptest.NewRequest(http.MethodPost, "/batch-download", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := h.BatchDownload()(c)
	if err == nil {
		t.Fatal("expected 400 Bad Request error for exceeding max batch download limit")
	}
	httpErr, ok := err.(*echo.HTTPError)
	if !ok || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400 Bad Request, got: %v", err)
	}
}

func TestBatchDownloadPartialFailureIsolation(t *testing.T) {
	job1 := &model.Job{OwnerID: "user-1", Filename: "success.m4a", Status: "완료"}
	job2 := &model.Job{OwnerID: "user-1", Filename: "corrupt.m4a", Status: "완료"}

	blobSvc := service.NewJobBlobService(service.JobBlobServiceDeps{
		LoadJobJSON: func(jobID, kind string) (string, error) {
			if jobID == "job-1" {
				return `{"transcript":"성공"}` , nil
			}
			return "", errors.New("simulated I/O corruption")
		},
	})

	h := LegacyJobsHandlers{
		CurrentUserOrUnauthorized: func(echo.Context) (*User, bool) { return &User{ID: "user-1"}, true },
		GetJob: func(id string) *model.Job {
			if id == "job-1" {
				return job1
			}
			if id == "job-2" {
				return job2
			}
			return nil
		},
		StatusCompleted: "완료",
		BlobSvc:         blobSvc,
	}

	e := echo.New()
	form := url.Values{}
	form.Add("job_ids", "job-1")
	form.Add("job_ids", "job-2")

	req := httptest.NewRequest(http.MethodPost, "/batch-download", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.BatchDownload()(c); err != nil {
		t.Fatalf("batch download handler failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	zipBytes := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("invalid zip archive output: %v", err)
	}

	entryNames := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		entryNames = append(entryNames, f.Name)
	}

	hasSuccess := false
	hasError := false
	for _, name := range entryNames {
		if name == "success.md" {
			hasSuccess = true
		}
		if strings.HasPrefix(name, "_ERROR_corrupt") {
			hasError = true
		}
	}

	if !hasSuccess || !hasError {
		t.Fatalf("expected both success.md and _ERROR_corrupt... in zip, got entries: %v", entryNames)
	}
}

