package httptransport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

func TestDetailJSONShowsOriginalTranscriptForRefineFailure(t *testing.T) {
	transcript := `{"segments":[{"from":"00:00:00,000","to":"00:00:01,000","text":"원본"}]}`
	blob := newTranscriptTestBlobService(transcript)
	job := &model.Job{OwnerID: "owner", FileType: "audio", Status: "정제 실패", StatusCode: model.JobStatusRefineFailedCode}
	h := JobDetailHandlers{
		CurrentUserOrUnauthorized: func(echo.Context) (*User, bool) { return &User{ID: "owner"}, true },
		CurrentUserName:           func(echo.Context) string { return "owner" },
		GetJob:                    func(string) *model.Job { return job },
		ToJobView:                 func(job *model.Job) any { return job },
		HasGeminiConfigured:       func() bool { return true },
		TagSvc: service.NewTagService(service.TagServiceDeps{
			ListTagsByOwner: func(string) ([]model.Tag, error) { return nil, nil },
		}),
		BlobSvc: blob,
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-1", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/jobs/:job_id")
	c.SetParamNames("job_id")
	c.SetParamValues("job-1")

	if err := h.DetailJSON()(c); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["view"] != "result" || payload["result_kind"] != "transcript_json" || payload["variant"] != "original" {
		t.Fatalf("unexpected refine failure payload: %v", payload)
	}
	if payload["download_text_url"] != "/download/job-1" {
		t.Fatalf("expected original download URL, got %v", payload["download_text_url"])
	}
}

func TestDownloadReturnsOriginalTranscriptForRefineFailure(t *testing.T) {
	blob := newTranscriptTestBlobService(`{"segments":[{"from":"00:00:00,000","to":"00:00:01,000","text":"원본 문장"}]}`)
	job := &model.Job{OwnerID: "owner", Filename: "lesson.wav", FileType: "audio", Status: "정제 실패", StatusCode: model.JobStatusRefineFailedCode}
	h := LegacyJobsHandlers{
		CurrentUserOrUnauthorized: func(echo.Context) (*User, bool) { return &User{ID: "owner"}, true },
		GetJob:                    func(string) *model.Job { return job },
		StatusCompleted:           "완료",
		BlobSvc:                   blob,
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/download/job-1", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/download/:job_id")
	c.SetParamNames("job_id")
	c.SetParamValues("job-1")

	if err := h.Download()(c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), "원본 문장") {
		t.Fatalf("expected original transcript download, got %q", rec.Body.String())
	}
}

func newTranscriptTestBlobService(transcript string) *service.JobBlobService {
	return service.NewJobBlobService(service.JobBlobServiceDeps{
		HasJobJSON: func(_ string, kind string) bool { return kind == "transcript_json" },
		LoadJobJSON: func(_ string, kind string) (string, error) {
			return transcript, nil
		},
		BlobKindTranscriptJSON: "transcript_json",
		BlobKindRefined:        "refined",
	})
}
