package app

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	httpx "whisperserver/src/internal/http"
	"whisperserver/src/internal/store"
)

func apiTrashListJSONHandler(c echo.Context) error {
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil
	}
	rows := buildJobRowsForUser(u.ID, strings.TrimSpace(c.QueryParam("q")), "", "", true)
	folders, _ := store.ListAllFoldersByOwner(u.ID, true)
	return c.JSON(http.StatusOK, map[string]any{
		"job_items": rows,
		"folders":   folders,
	})
}

func apiRestoreJobJSONHandler(c echo.Context) error {
	u, job, err := requireOwnedJobOrNotFound(c, true)
	if err != nil {
		return err
	}
	jobID := c.Param("job_id")
	folderID := httpx.EnsureRestoredFolder(u.ID, job.FolderID, procLogf, procErrf, "api.job.restore")
	updates := map[string]any{"is_trashed": false, "deleted_at": ""}
	updates["folder_id"] = folderID
	setJobFields(jobID, updates)
	job = getJob(jobID)
	httpx.ResumeRestoredJob(jobID, job, func(jobID string) bool {
		return store.HasJobBlob(jobID, store.BlobKindAudioAAC)
	}, store.HasJobBlob, setJobFields, enqueueTranscribe, enqueueRefine, enqueuePDFExtract, statusPending, statusRefiningPending)
	httpx.TouchFolderAncestors(u.ID, folderID, procErrf, "api.job.restoreTouchFolder", "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, folderID)
	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "restored"})
}

func apiRestoreFolderJSONHandler(c echo.Context) error {
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil
	}
	folderID := c.Param("folder_id")
	if err := store.SetFolderTrashed(u.ID, folderID, false); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 복구 실패")
	}
	if f, err := store.GetFolderByID(u.ID, folderID); err == nil {
		httpx.TouchFolderAncestors(u.ID, f.ParentID, procErrf, "api.folder.restoreTouchParent", "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
	}
	notifyFilesChanged(u.ID)
	return c.JSON(http.StatusOK, map[string]string{"folder_id": folderID, "status": "restored"})
}

func clearTrashJSONHandler(c echo.Context) error {
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil
	}

	snapshot := jobsSnapshot()
	toDelete := make([]string, 0)
	for id, job := range snapshot {
		if job.OwnerID == u.ID && job.IsTrashed {
			toDelete = append(toDelete, id)
		}
	}
	deleteJobs(toDelete)
	if err := store.DeleteTrashedFoldersByOwner(u.ID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "휴지통 비우기 실패")
	}
	notifyFilesChanged(u.ID)
	return c.JSON(http.StatusOK, map[string]any{
		"deleted_jobs": len(toDelete),
		"status":       "cleared",
	})
}

func deleteTrashJobsJSONHandler(c echo.Context) error {
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil
	}

	var body struct {
		JobIDs []string `json:"job_ids"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}

	snapshot := jobsSnapshot()
	toDelete := make([]string, 0, len(body.JobIDs))
	for _, id := range body.JobIDs {
		id = strings.TrimSpace(id)
		job := snapshot[id]
		if job == nil || job.OwnerID != u.ID || !job.IsTrashed {
			continue
		}
		toDelete = append(toDelete, id)
	}

	deleteJobs(toDelete)
	notifyFilesChanged(u.ID)
	return c.JSON(http.StatusOK, map[string]any{
		"deleted_jobs": len(toDelete),
		"job_ids":      toDelete,
	})
}
