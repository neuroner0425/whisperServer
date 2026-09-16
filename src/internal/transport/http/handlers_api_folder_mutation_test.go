package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

func TestFolderTrashMovesToTrash(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/folders/f1", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/folders/:folder_id")
	c.SetParamNames("folder_id")
	c.SetParamValues("f1")

	user := &User{ID: "u1"}
	folder := &model.Folder{ID: "f1", OwnerID: "u1", Name: "TestFolder"}

	var trashedSubtree map[string]struct{}
	var filesNotified string

	folderSvc := service.NewFolderService(service.FolderServiceDeps{
		GetFolderByID: func(ownerID, folderID string) (*model.Folder, error) {
			if folderID == "f1" {
				return folder, nil
			}
			return nil, nil
		},
		SetFolderTrashed: func(ownerID, folderID string, trashed bool) error {
			folder.IsTrashed = trashed
			return nil
		},
	})

	handler := FolderMutationHandlers{
		CurrentUserOrUnauthorized: func(c echo.Context) (*User, bool) {
			return user, true
		},
		NotifyFilesChanged: func(userID string) {
			filesNotified = userID
		},
		FolderSvc: folderSvc,
		CollectFolderSubtree: func(userID string, folderIDs []string, trashFolders bool) map[string]struct{} {
			return map[string]struct{}{"f1": {}}
		},
		MarkSubtreeJobsTrashed: func(userID string, subtree map[string]struct{}) {
			trashedSubtree = subtree
		},
	}.Trash()

	if err := handler(c); err != nil {
		t.Fatalf("handler failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if trashedSubtree == nil || len(trashedSubtree) != 1 {
		t.Fatalf("expected subtree to be trashed, got %v", trashedSubtree)
	}
	if filesNotified != "u1" {
		t.Fatalf("expected files changed notification for u1, got %s", filesNotified)
	}
}
