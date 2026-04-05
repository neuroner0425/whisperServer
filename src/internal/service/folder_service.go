package service

import (
	"database/sql"
	"net/http"
	"strings"

	"whisperserver/src/internal/model"
)

type FolderServiceDeps struct {
	GetFolderByID               func(ownerID, folderID string) (*model.Folder, error)
	CreateFolder                func(ownerID, name, parentID string) (string, error)
	RenameFolder                func(ownerID, folderID, newName string) error
	SetFolderTrashed            func(ownerID, folderID string, trashed bool) error
	MoveFolder                  func(ownerID, folderID, parentID string) error
	TouchFolderAndAncestors     func(ownerID, folderID string) error
	IsFolderDescendant          func(ownerID, folderID, maybeDescendantID string) (bool, error)
	ListAllFoldersByOwner       func(ownerID string, trashed bool) ([]model.Folder, error)
	ListFolderPath              func(ownerID, folderID string) ([]model.Folder, error)
	DeleteTrashedFoldersByOwner func(ownerID string) error
}

type FolderService struct {
	d FolderServiceDeps
}

func NewFolderService(d FolderServiceDeps) *FolderService {
	return &FolderService{d: d}
}

func (s *FolderService) NormalizeID(v string) string {
	return strings.TrimSpace(v)
}

func (s *FolderService) Require(ownerID, folderID string, allowTrashed bool, statusCode int, message string) (*model.Folder, error) {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return nil, NewHTTPError(statusCode, message)
	}
	if s.d.GetFolderByID == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	f, err := s.d.GetFolderByID(ownerID, folderID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NewHTTPError(statusCode, message)
		}
		return nil, NewHTTPError(http.StatusInternalServerError, "폴더 조회 실패")
	}
	if f == nil {
		return nil, NewHTTPError(statusCode, message)
	}
	if !allowTrashed && f.IsTrashed {
		return nil, NewHTTPError(statusCode, message)
	}
	return f, nil
}

func (s *FolderService) Create(ownerID, name, parentID string) (string, error) {
	name = strings.TrimSpace(name)
	parentID = strings.TrimSpace(parentID)
	if name == "" {
		return "", NewHTTPError(http.StatusBadRequest, "폴더명을 입력하세요.")
	}
	if s.d.CreateFolder == nil {
		return "", NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	id, err := s.d.CreateFolder(ownerID, name, parentID)
	if err != nil {
		return "", NewHTTPError(http.StatusBadRequest, "폴더 생성 실패(중복 이름 확인)")
	}
	return id, nil
}

func (s *FolderService) Rename(ownerID, folderID, newName string) error {
	folderID = strings.TrimSpace(folderID)
	newName = strings.TrimSpace(newName)
	if folderID == "" {
		return NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	if newName == "" {
		return NewHTTPError(http.StatusBadRequest, "새 폴더명을 입력하세요.")
	}
	if s.d.RenameFolder == nil {
		return NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	if err := s.d.RenameFolder(ownerID, folderID, newName); err != nil {
		return NewHTTPError(http.StatusBadRequest, "폴더 이름 변경 실패(중복 이름 확인)")
	}
	return nil
}

func (s *FolderService) Trash(ownerID, folderID string) error {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return NewHTTPError(http.StatusBadRequest, "폴더 삭제 실패")
	}
	if s.d.SetFolderTrashed == nil {
		return NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	if err := s.d.SetFolderTrashed(ownerID, folderID, true); err != nil {
		return NewHTTPError(http.StatusBadRequest, "폴더 삭제 실패")
	}
	return nil
}

func (s *FolderService) Restore(ownerID, folderID string) (*model.Folder, error) {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "폴더 복구 실패")
	}
	if s.d.SetFolderTrashed == nil || s.d.GetFolderByID == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	if err := s.d.SetFolderTrashed(ownerID, folderID, false); err != nil {
		return nil, NewHTTPError(http.StatusBadRequest, "폴더 복구 실패")
	}
	f, err := s.d.GetFolderByID(ownerID, folderID)
	if err != nil {
		return nil, NewHTTPError(http.StatusInternalServerError, "폴더 조회 실패")
	}
	return f, nil
}

func (s *FolderService) Move(ownerID, folderID, parentID string) error {
	folderID = strings.TrimSpace(folderID)
	parentID = strings.TrimSpace(parentID)
	if folderID == "" {
		return NewHTTPError(http.StatusBadRequest, "폴더 이동 실패")
	}
	if s.d.MoveFolder == nil {
		return NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	if err := s.d.MoveFolder(ownerID, folderID, parentID); err != nil {
		return NewHTTPError(http.StatusBadRequest, "폴더 이동 실패")
	}
	return nil
}

func (s *FolderService) IsDescendant(ownerID, folderID, maybeDescendantID string) (bool, error) {
	if s.d.IsFolderDescendant == nil {
		return false, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	return s.d.IsFolderDescendant(ownerID, folderID, maybeDescendantID)
}

func (s *FolderService) TouchAncestors(ownerID, folderID string) error {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return nil
	}
	if s.d.TouchFolderAndAncestors == nil {
		return NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	return s.d.TouchFolderAndAncestors(ownerID, folderID)
}

func (s *FolderService) ListAll(ownerID string, trashed bool) ([]model.Folder, error) {
	if s.d.ListAllFoldersByOwner == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	folders, err := s.d.ListAllFoldersByOwner(ownerID, trashed)
	if err != nil {
		return nil, NewHTTPError(http.StatusInternalServerError, "폴더 조회 실패")
	}
	return folders, nil
}

func (s *FolderService) Path(ownerID, folderID string) ([]model.Folder, error) {
	if s.d.ListFolderPath == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	path, err := s.d.ListFolderPath(ownerID, strings.TrimSpace(folderID))
	if err != nil {
		return nil, NewHTTPError(http.StatusInternalServerError, "폴더 조회 실패")
	}
	return path, nil
}

func (s *FolderService) DeleteTrashed(ownerID string) error {
	if s.d.DeleteTrashedFoldersByOwner == nil {
		return NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	if err := s.d.DeleteTrashedFoldersByOwner(ownerID); err != nil {
		return NewHTTPError(http.StatusInternalServerError, "휴지통 비우기 실패")
	}
	return nil
}

// EnsureRestored returns a usable folder id for a restored job.
// If the folder exists, it restores it from trash (best-effort) and returns the id.
// If the folder is missing, it creates a new root folder and returns its id.
func (s *FolderService) EnsureRestored(ownerID, folderID string, logf func(string, ...any), errf func(string, error, string, ...any), scopePrefix string) string {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return ""
	}
	if s.d.GetFolderByID == nil || s.d.CreateFolder == nil || s.d.SetFolderTrashed == nil {
		return ""
	}
	folder, err := s.d.GetFolderByID(ownerID, folderID)
	if err == nil && folder != nil {
		if folder.IsTrashed {
			if err := s.d.SetFolderTrashed(ownerID, folderID, false); err != nil && errf != nil {
				errf(scopePrefix+".restoreFolder", err, "owner_id=%s folder_id=%s", ownerID, folderID)
			}
		}
		return folderID
	}
	newID, err := s.d.CreateFolder(ownerID, "복구된 폴더", "")
	if err != nil {
		if errf != nil {
			errf(scopePrefix+".createFolder", err, "owner_id=%s missing_folder_id=%s", ownerID, folderID)
		}
		return ""
	}
	if logf != nil {
		logf("[RESTORE] created_folder owner_id=%s missing_folder_id=%s new_folder_id=%s", ownerID, folderID, newID)
	}
	return newID
}
