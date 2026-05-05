package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

// FolderMutationHandlers serves folder create, rename, and trash API calls.
type FolderMutationHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	NotifyFilesChanged        func(string)
	FolderSvc                 *service.FolderService

	CollectFolderSubtree   func(userID string, folderIDs []string, trashFolders bool) map[string]struct{}
	MarkSubtreeJobsTrashed func(userID string, subtree map[string]struct{})
	JobsSnapshot           func() map[string]*model.Job
	DeleteJobsFn           func([]string)

	Errf func(scope string, err error, format string, args ...any)
}

// Create adds a new folder under the requested parent.
func (h FolderMutationHandlers) Create() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.NotifyFilesChanged == nil || h.FolderSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		var body struct {
			Name     string `json:"name"`
			ParentID string `json:"parent_id"`
		}
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
		}
		name := strings.TrimSpace(body.Name)
		parentID := h.FolderSvc.NormalizeID(body.ParentID)
		if parentID != "" {
			if _, err := h.FolderSvc.Require(u.ID, parentID, false, http.StatusBadRequest, "유효하지 않은 상위 폴더입니다."); err != nil {
				return toEchoHTTPError(err, http.StatusBadRequest, "유효하지 않은 상위 폴더입니다.")
			}
		}
		id, err := h.FolderSvc.Create(u.ID, name, parentID)
		if err != nil {
			return toEchoHTTPError(err, http.StatusBadRequest, "폴더 생성 실패(중복 이름 확인)")
		}
		if err := h.FolderSvc.TouchAncestors(u.ID, parentID); err != nil && h.Errf != nil {
			h.Errf("api.folder.createTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, id, parentID)
		}
		h.NotifyFilesChanged(u.ID)
		return c.JSON(http.StatusOK, map[string]any{
			"folder_id": id,
			"name":      name,
			"parent_id": parentID,
		})
	}
}

// Rename changes a folder name while preserving its place in the tree.
func (h FolderMutationHandlers) Rename() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.NotifyFilesChanged == nil || h.FolderSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		folderID := c.Param("folder_id")
		var body struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
		}
		newName := strings.TrimSpace(body.Name)
		f, err := h.FolderSvc.Require(u.ID, folderID, false, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		if err != nil {
			return toEchoHTTPError(err, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		}
		if err := h.FolderSvc.Rename(u.ID, folderID, newName); err != nil {
			return toEchoHTTPError(err, http.StatusBadRequest, "폴더 이름 변경 실패(중복 이름 확인)")
		}
		if f != nil {
			if err := h.FolderSvc.TouchAncestors(u.ID, f.ParentID); err != nil && h.Errf != nil {
				h.Errf("api.folder.renameTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
			}
		}
		h.NotifyFilesChanged(u.ID)
		return c.JSON(http.StatusOK, map[string]string{"folder_id": folderID, "name": newName})
	}
}

// Trash permanently deletes a folder subtree and all jobs inside it.
func (h FolderMutationHandlers) Trash() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.NotifyFilesChanged == nil || h.CollectFolderSubtree == nil || h.JobsSnapshot == nil || h.DeleteJobsFn == nil || h.FolderSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		folderID := c.Param("folder_id")
		f, _ := h.FolderSvc.Require(u.ID, folderID, true, http.StatusBadRequest, "폴더 삭제 실패")
		subtree := h.CollectFolderSubtree(u.ID, []string{folderID}, false)
		jobIDs := make([]string, 0)
		for id, job := range h.JobsSnapshot() {
			if job == nil || job.OwnerID != u.ID {
				continue
			}
			if _, ok := subtree[strings.TrimSpace(job.FolderID)]; ok {
				jobIDs = append(jobIDs, id)
			}
		}
		h.DeleteJobsFn(jobIDs)
		if err := h.FolderSvc.DeleteSubtree(u.ID, folderID); err != nil {
			return toEchoHTTPError(err, http.StatusBadRequest, "폴더 삭제 실패")
		}
		if f != nil {
			if err := h.FolderSvc.TouchAncestors(u.ID, f.ParentID); err != nil && h.Errf != nil {
				h.Errf("api.folder.deleteTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
			}
		}
		h.NotifyFilesChanged(u.ID)
		return c.JSON(http.StatusOK, map[string]any{"folder_id": folderID, "deleted_jobs": len(jobIDs), "status": "deleted"})
	}
}
