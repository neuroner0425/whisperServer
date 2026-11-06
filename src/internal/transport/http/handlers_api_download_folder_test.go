package httptransport

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

func TestDownloadFolderStreamingSuccess(t *testing.T) {
	job1 := &model.Job{OwnerID: "user-1", Filename: "folder_note.m4a", Status: "완료"}

	blobSvc := service.NewJobBlobService(service.JobBlobServiceDeps{
		HasJobBlob: func(jobID, kind string) bool { return false },
		LoadJobJSON: func(jobID, kind string) (string, error) {
			if jobID == "job-f1" {
				return `{"transcript":"폴더 회의 내용"}`, nil
			}
			return "", errors.New("not found")
		},
	})

	folderSvc := service.NewFolderService(service.FolderServiceDeps{
		GetFolderByID: func(ownerID, folderID string) (*model.Folder, error) {
			return &model.Folder{ID: "f-123", Name: "Projects", OwnerID: "user-1"}, nil
		},
	})

	h := FolderDownloadHandlers{
		CurrentUserOrUnauthorized: func(echo.Context) (*User, bool) { return &User{ID: "user-1"}, true },
		FolderSvc:                 folderSvc,
		BlobSvc:                   blobSvc,
		CollectFolderSubtree: func(userID string, folderIDs []string, trashFolders bool) map[string]struct{} {
			return map[string]struct{}{"f-123": {}}
		},
		ListCompletedJobsByFolderIDs: func(ownerID string, folderIDs []string) ([]CompletedJobRecord, error) {
			return []CompletedJobRecord{
				{ID: "job-f1", Job: job1},
			}, nil
		},
		StatusCompleted: "완료",
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/folders/f-123/download", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("folder_id")
	c.SetParamValues("f-123")

	handler := h.Handler()
	if err := handler(c); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("failed to read response zip: %v", err)
	}

	if len(zr.File) != 1 {
		t.Fatalf("expected 1 file in zip, got %d", len(zr.File))
	}

	if zr.File[0].Name != "folder_note.md" {
		t.Fatalf("expected folder_note.md, got %s", zr.File[0].Name)
	}

	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatalf("failed to open zip entry: %v", err)
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("failed to read zip entry: %v", err)
	}
	if len(content) == 0 || !strings.Contains(string(content), "# folder_note") {
		t.Fatalf("expected zip content to contain '# folder_note', got: %s", string(content))
	}
}

func TestDownloadFolderPartialFailureIsolation(t *testing.T) {
	job1 := &model.Job{OwnerID: "user-1", Filename: "good.m4a", Status: "완료"}
	job2 := &model.Job{OwnerID: "user-1", Filename: "bad.m4a", Status: "완료"}

	blobSvc := service.NewJobBlobService(service.JobBlobServiceDeps{
		HasJobBlob: func(jobID, kind string) bool { return false },
		LoadJobJSON: func(jobID, kind string) (string, error) {
			if jobID == "job-good" {
				return `{"transcript":"성공 내용"}`, nil
			}
			return "", errors.New("blob corrupted")
		},
	})

	folderSvc := service.NewFolderService(service.FolderServiceDeps{
		GetFolderByID: func(ownerID, folderID string) (*model.Folder, error) {
			return &model.Folder{ID: "f-123", Name: "Projects", OwnerID: "user-1"}, nil
		},
	})

	h := FolderDownloadHandlers{
		CurrentUserOrUnauthorized: func(echo.Context) (*User, bool) { return &User{ID: "user-1"}, true },
		FolderSvc:                 folderSvc,
		BlobSvc:                   blobSvc,
		CollectFolderSubtree: func(userID string, folderIDs []string, trashFolders bool) map[string]struct{} {
			return map[string]struct{}{"f-123": {}}
		},
		ListCompletedJobsByFolderIDs: func(ownerID string, folderIDs []string) ([]CompletedJobRecord, error) {
			return []CompletedJobRecord{
				{ID: "job-good", Job: job1},
				{ID: "job-bad", Job: job2},
			}, nil
		},
		StatusCompleted: "완료",
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/folders/f-123/download", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("folder_id")
	c.SetParamValues("f-123")

	handler := h.Handler()
	if err := handler(c); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("failed to read response zip: %v", err)
	}

	var entryNames []string
	for _, f := range zr.File {
		entryNames = append(entryNames, f.Name)
	}

	hasGood := false
	hasBadErr := false
	for _, name := range entryNames {
		if name == "good.md" {
			hasGood = true
		}
		if strings.HasPrefix(name, "_ERROR_bad") {
			hasBadErr = true
		}
	}

	if !hasGood || !hasBadErr {
		t.Fatalf("expected both good.md and _ERROR_bad... in zip, got entries: %v", entryNames)
	}
}
