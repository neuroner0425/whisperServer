package service

import (
	"database/sql"
	"testing"

	model "whisperserver/src/internal/domain"
)

func TestFolderService_Require_NotFound(t *testing.T) {
	s := NewFolderService(FolderServiceDeps{
		GetFolderByID: func(string, string) (*model.Folder, error) {
			return nil, sql.ErrNoRows
		},
	})

	_, err := s.Require("u1", "missing", false, 404, "폴더를 찾을 수 없습니다.")
	if err == nil {
		t.Fatalf("expected err")
	}
	httpErr, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("expected *HTTPError, got %T", err)
	}
	if httpErr.Status != 404 {
		t.Fatalf("status=%d", httpErr.Status)
	}
}

func TestFolderService_EnsureRestored_ExistingTrashedFolder(t *testing.T) {
	called := false
	s := NewFolderService(FolderServiceDeps{
		GetFolderByID: func(string, string) (*model.Folder, error) {
			return &model.Folder{ID: "f1", IsTrashed: true}, nil
		},
		SetFolderTrashed: func(string, string, bool) error {
			called = true
			return nil
		},
		CreateFolder: func(string, string, string) (string, error) {
			t.Fatalf("unexpected CreateFolder call")
			return "", nil
		},
	})
	id := s.EnsureRestored("u1", "f1", nil, nil, "scope")
	if id != "f1" {
		t.Fatalf("id=%q", id)
	}
	if !called {
		t.Fatalf("expected SetFolderTrashed called")
	}
}

func TestFolderService_EnsureRestored_MissingFolderCreatesNew(t *testing.T) {
	s := NewFolderService(FolderServiceDeps{
		GetFolderByID: func(string, string) (*model.Folder, error) {
			return nil, sql.ErrNoRows
		},
		SetFolderTrashed: func(string, string, bool) error {
			t.Fatalf("unexpected SetFolderTrashed call")
			return nil
		},
		CreateFolder: func(ownerID, name, parentID string) (string, error) {
			if ownerID != "u1" || name == "" || parentID != "" {
				t.Fatalf("unexpected args ownerID=%q name=%q parentID=%q", ownerID, name, parentID)
			}
			return "newf", nil
		},
	})
	id := s.EnsureRestored("u1", "missing", nil, nil, "scope")
	if id != "newf" {
		t.Fatalf("id=%q", id)
	}
}
