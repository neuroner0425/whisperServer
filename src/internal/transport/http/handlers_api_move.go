package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

// MoveHandlers moves jobs and folders between folders in a single request.
type MoveHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	FolderSvc                 *service.FolderService
	GetJob                    func(string) *model.Job
	SetJobFields              func(string, map[string]any)
	NotifyFilesChanged        func(string)
	Errf                      func(scope string, err error, format string, args ...any)
}

// BatchMove updates folder placement for the selected jobs and folders.
func (h MoveHandlers) BatchMove() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.FolderSvc == nil || h.GetJob == nil || h.SetJobFields == nil || h.NotifyFilesChanged == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		var body struct {
			JobIDs         []string `json:"job_ids"`
			FolderIDs      []string `json:"folder_ids"`
			TargetFolderID string   `json:"target_folder_id"`
		}
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
		}
		targetFolder := strings.TrimSpace(body.TargetFolderID)
		if targetFolder != "" {
			if _, err := h.FolderSvc.Require(u.ID, targetFolder, false, http.StatusBadRequest, "유효하지 않은 대상 폴더입니다."); err != nil {
				return toEchoHTTPError(err, http.StatusBadRequest, "유효하지 않은 대상 폴더입니다.")
			}
		}

		// Track folders whose aggregate counters or timestamps need refreshing.
		touchedFolders := map[string]struct{}{}
		for _, id := range body.JobIDs {
			job := h.GetJob(id)
			if job != nil && job.OwnerID == u.ID && !job.IsTrashed {
				if job.FolderID != "" {
					touchedFolders[job.FolderID] = struct{}{}
				}
				if targetFolder != "" {
					touchedFolders[targetFolder] = struct{}{}
				}
				h.SetJobFields(id, map[string]any{"folder_id": targetFolder})
			}
		}

		// Move folders after jobs so ancestor validation sees the original tree.
		for _, id := range body.FolderIDs {
			id = strings.TrimSpace(id)
			if id == "" || id == targetFolder {
				continue
			}
			f, err := h.FolderSvc.Require(u.ID, id, false, http.StatusBadRequest, "유효하지 않은 폴더입니다.")
			if err != nil {
				continue
			}
			if targetFolder != "" {
				descendant, err := h.FolderSvc.IsDescendant(u.ID, id, targetFolder)
				if err != nil || descendant {
					continue
				}
			}
			if err := h.FolderSvc.Move(u.ID, id, targetFolder); err != nil {
				continue
			}
			if f.ParentID != "" {
				touchedFolders[f.ParentID] = struct{}{}
			}
			if targetFolder != "" {
				touchedFolders[targetFolder] = struct{}{}
			}
		}

		for id := range touchedFolders {
			if err := h.FolderSvc.TouchAncestors(u.ID, id); err != nil && h.Errf != nil {
				h.Errf("api.batchMove.touchFolder", err, "owner_id=%s folder_id=%s target_folder_id=%s", u.ID, id, targetFolder)
			}
		}
		h.NotifyFilesChanged(u.ID)
		return c.JSON(http.StatusOK, map[string]any{
			"target_folder_id": targetFolder,
			"job_ids":          body.JobIDs,
			"folder_ids":       body.FolderIDs,
		})
	}
}
