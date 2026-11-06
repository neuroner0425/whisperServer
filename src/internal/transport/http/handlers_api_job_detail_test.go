package httptransport

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
	"whisperserver/src/internal/service"
)

func newTestStreamingBlobService(audioData, pdfData []byte) *service.JobBlobService {
	return service.NewJobBlobService(service.JobBlobServiceDeps{
		BlobKindAudioAAC:    "audio_aac",
		BlobKindPDFOriginal: "pdf_original",
		OpenJobBlob: func(jobID, kind string) (storage.ReadSeekCloser, storage.ObjectInfo, error) {
			var data []byte
			switch kind {
			case "audio_aac":
				data = audioData
			case "pdf_original":
				data = pdfData
			default:
				return nil, storage.ObjectInfo{}, storage.ErrNotFound
			}
			info := storage.ObjectInfo{
				Key:          jobID + "/" + kind,
				Size:         int64(len(data)),
				LastModified: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
			}
			return storage.NopReadSeekCloser(bytes.NewReader(data)), info, nil
		},
	})
}

func TestAudioRangeStreaming(t *testing.T) {
	audioContent := []byte("0123456789abcdef") // 16 bytes
	blobSvc := newTestStreamingBlobService(audioContent, nil)
	job := &model.Job{OwnerID: "owner-1", FileType: "audio"}

	h := JobDetailHandlers{
		CurrentUserOrUnauthorized: func(echo.Context) (*User, bool) { return &User{ID: "owner-1"}, true },
		GetJob:                    func(string) *model.Job { return job },
		BlobSvc:                   blobSvc,
	}

	e := echo.New()

	// 1. Full request without Range header -> 200 OK
	{
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-audio-1/audio", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/jobs/:job_id/audio")
		c.SetParamNames("job_id")
		c.SetParamValues("job-audio-1")

		if err := h.Audio()(c); err != nil {
			t.Fatalf("audio handler failed: %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", rec.Code)
		}
		if rec.Header().Get("Accept-Ranges") != "bytes" {
			t.Fatalf("expected Accept-Ranges: bytes, got %q", rec.Header().Get("Accept-Ranges"))
		}
		if rec.Body.String() != string(audioContent) {
			t.Fatalf("body mismatch: %s != %s", rec.Body.String(), audioContent)
		}
	}

	// 2. Partial Content request: Range: bytes=0-3 -> 206 Partial Content
	{
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-audio-1/audio", nil)
		req.Header.Set("Range", "bytes=0-3")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/jobs/:job_id/audio")
		c.SetParamNames("job_id")
		c.SetParamValues("job-audio-1")

		if err := h.Audio()(c); err != nil {
			t.Fatalf("audio handler with range failed: %v", err)
		}
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("expected 206 Partial Content, got %d", rec.Code)
		}
		if rec.Header().Get("Content-Range") != "bytes 0-3/16" {
			t.Fatalf("expected Content-Range: bytes 0-3/16, got %q", rec.Header().Get("Content-Range"))
		}
		if rec.Body.String() != "0123" {
			t.Fatalf("expected body '0123', got %q", rec.Body.String())
		}
	}

	// 3. Partial Content request: Range: bytes=10-15 -> 206 Partial Content
	{
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-audio-1/audio", nil)
		req.Header.Set("Range", "bytes=10-15")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/jobs/:job_id/audio")
		c.SetParamNames("job_id")
		c.SetParamValues("job-audio-1")

		if err := h.Audio()(c); err != nil {
			t.Fatalf("audio handler with range failed: %v", err)
		}
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("expected 206 Partial Content, got %d", rec.Code)
		}
		if rec.Header().Get("Content-Range") != "bytes 10-15/16" {
			t.Fatalf("expected Content-Range: bytes 10-15/16, got %q", rec.Header().Get("Content-Range"))
		}
		if rec.Body.String() != "abcdef" {
			t.Fatalf("expected body 'abcdef', got %q", rec.Body.String())
		}
	}
}

func TestPDFRangeStreaming(t *testing.T) {
	pdfContent := []byte("%PDF-1.4-sample-test-document-binary")
	blobSvc := newTestStreamingBlobService(nil, pdfContent)
	job := &model.Job{OwnerID: "owner-1", FileType: "pdf"}

	h := JobDetailHandlers{
		CurrentUserOrUnauthorized: func(echo.Context) (*User, bool) { return &User{ID: "owner-1"}, true },
		GetJob:                    func(string) *model.Job { return job },
		BlobSvc:                   blobSvc,
	}

	e := echo.New()

	// Range: bytes=0-4 -> "%PDF-"
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-pdf-1/pdf", nil)
	req.Header.Set("Range", "bytes=0-4")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/jobs/:job_id/pdf")
	c.SetParamNames("job_id")
	c.SetParamValues("job-pdf-1")

	if err := h.PDF()(c); err != nil {
		t.Fatalf("pdf handler failed: %v", err)
	}
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content, got %d", rec.Code)
	}
	if rec.Body.String() != "%PDF-" {
		t.Fatalf("expected body '%%PDF-', got %q", rec.Body.String())
	}
}
