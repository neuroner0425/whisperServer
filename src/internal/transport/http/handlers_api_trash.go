package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"whisperserver/src/internal/model"
	"whisperserver/src/internal/service"
)

type TrashHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	FolderSvc                 *service.FolderService
	BlobSvc                   *service.JobBlobService

	BuildJobRowsForUser func(userID, q, tag, folderID string, trashed bool) []JobRow

	JobsSnapshot func() map[string]*model.Job
	GetJob       func(string) *model.Job
	SetJobFields func(string, map[string]any)
	DeleteJobsFn func([]string)

	EnqueueTranscribe func(string)
	EnqueueRefine     func(string)
	EnqueuePDFExtract func(string)

	NotifyFilesChanged func(string)

	StatusPending         string
	StatusRefiningPending string

	Logf func(string, ...any)
	Errf func(string, error, string, ...any)
}

func (h TrashHandlers) List() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.FolderSvc == nil || h.BuildJobRowsForUser == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		rows := h.BuildJobRowsForUser(u.ID, strings.TrimSpace(c.QueryParam("q")), "", "", true)
		folders, _ := h.FolderSvc.ListAll(u.ID, true)
		return c.JSON(http.StatusOK, map[string]any{
			"job_items": rows,
			"folders":   folders,
		})
	}
}

func (h TrashHandlers) RestoreJob() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.FolderSvc == nil || h.BlobSvc == nil || h.GetJob == nil || h.SetJobFields == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		jobID := strings.TrimSpace(c.Param("job_id"))
		job := h.GetJob(jobID)
		if job == nil || job.OwnerID != u.ID {
			return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
		}

		folderID := h.FolderSvc.EnsureRestored(u.ID, job.FolderID, h.Logf, h.Errf, "api.job.restore")
		updates := map[string]any{"is_trashed": false, "deleted_at": "", "folder_id": folderID}
		h.SetJobFields(jobID, updates)
		job = h.GetJob(jobID)

		service.ResumeRestoredJob(
			jobID,
			job,
			h.BlobSvc,
			h.SetJobFields,
			h.EnqueueTranscribe,
			h.EnqueueRefine,
			h.EnqueuePDFExtract,
			h.StatusPending,
			h.StatusRefiningPending,
		)
		if err := h.FolderSvc.TouchAncestors(u.ID, folderID); err != nil && h.Errf != nil {
			h.Errf("api.job.restoreTouchFolder", err, "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, folderID)
		}
		return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "restored"})
	}
}

func (h TrashHandlers) RestoreFolder() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.NotifyFilesChanged == nil || h.FolderSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		folderID := strings.TrimSpace(c.Param("folder_id"))
		f, err := h.FolderSvc.Restore(u.ID, folderID)
		if err != nil {
			return toEchoHTTPError(err, http.StatusBadRequest, "폴더 복구 실패")
		}
		if f != nil {
			if err := h.FolderSvc.TouchAncestors(u.ID, f.ParentID); err != nil && h.Errf != nil {
				h.Errf("api.folder.restoreTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
			}
		}
		h.NotifyFilesChanged(u.ID)
		return c.JSON(http.StatusOK, map[string]string{"folder_id": folderID, "status": "restored"})
	}
}

func (h TrashHandlers) Clear() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.FolderSvc == nil || h.JobsSnapshot == nil || h.DeleteJobsFn == nil || h.NotifyFilesChanged == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}

		snapshot := h.JobsSnapshot()
		toDelete := make([]string, 0)
		for id, job := range snapshot {
			if job != nil && job.OwnerID == u.ID && job.IsTrashed {
				toDelete = append(toDelete, id)
			}
		}
		h.DeleteJobsFn(toDelete)
		if err := h.FolderSvc.DeleteTrashed(u.ID); err != nil {
			return toEchoHTTPError(err, http.StatusInternalServerError, "휴지통 비우기 실패")
		}
		h.NotifyFilesChanged(u.ID)
		return c.JSON(http.StatusOK, map[string]any{
			"deleted_jobs": len(toDelete),
			"status":       "cleared",
		})
	}
}

func (h TrashHandlers) DeleteTrashJobs() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.JobsSnapshot == nil || h.DeleteJobsFn == nil || h.NotifyFilesChanged == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}

		var body struct {
			JobIDs []string `json:"job_ids"`
		}
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
		}

		snapshot := h.JobsSnapshot()
		toDelete := make([]string, 0, len(body.JobIDs))
		for _, id := range body.JobIDs {
			id = strings.TrimSpace(id)
			job := snapshot[id]
			if job == nil || job.OwnerID != u.ID || !job.IsTrashed {
				continue
			}
			toDelete = append(toDelete, id)
		}

		h.DeleteJobsFn(toDelete)
		h.NotifyFilesChanged(u.ID)
		return c.JSON(http.StatusOK, map[string]any{
			"deleted_jobs": len(toDelete),
			"job_ids":      toDelete,
		})
	}
}
