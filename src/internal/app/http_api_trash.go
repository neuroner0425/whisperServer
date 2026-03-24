package app

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/store"
)

func apiTrashListJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	rows := buildJobRowsForUser(u.ID, strings.TrimSpace(c.QueryParam("q")), "", "", true)
	folders, _ := store.ListAllFoldersByOwner(u.ID, true)
	return c.JSON(http.StatusOK, map[string]any{
		"job_items": rows,
		"folders":   folders,
	})
}

func apiRestoreJobJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	folderID := normalizeFolderID(job.FolderID)
	updates := map[string]any{"is_trashed": false, "deleted_at": ""}
	if folderID != "" {
		f, ferr := store.GetFolderByID(u.ID, folderID)
		if ferr == nil {
			if f.IsTrashed {
				_ = store.SetFolderTrashed(u.ID, folderID, false)
			}
		} else {
			newID, err := store.CreateFolder(u.ID, "복구된 폴더", "")
			if err == nil {
				updates["folder_id"] = newID
				folderID = newID
			} else {
				updates["folder_id"] = ""
				folderID = ""
			}
		}
	}
	setJobFields(jobID, updates)
	job = getJob(jobID)
	if job != nil {
		if store.HasJobBlob(jobID, store.BlobKindAudioAAC) && !store.HasJobBlob(jobID, store.BlobKindTranscript) {
			setJobFields(jobID, map[string]any{
				"status":           statusPending,
				"phase":            "",
				"progress_percent": 0,
				"progress_label":   "",
				"started_at":       "",
				"started_ts":       0,
				"completed_at":     "",
				"completed_ts":     0,
				"duration":         "",
				"status_detail":    "",
			})
			enqueueTranscribe(jobID)
		} else if store.HasJobBlob(jobID, store.BlobKindTranscript) && !store.HasJobBlob(jobID, store.BlobKindRefined) && job.RefineEnabled {
			setJobFields(jobID, map[string]any{
				"status":         statusRefiningPending,
				"progress_label": "",
				"completed_at":   "",
				"completed_ts":   0,
				"duration":       "",
				"status_detail":  "",
			})
			enqueueRefine(jobID)
		}
	}
	_ = store.TouchFolderAndAncestors(u.ID, folderID)
	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "restored"})
}

func apiRestoreFolderJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	folderID := c.Param("folder_id")
	if err := store.SetFolderTrashed(u.ID, folderID, false); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 복구 실패")
	}
	if f, err := store.GetFolderByID(u.ID, folderID); err == nil {
		_ = store.TouchFolderAndAncestors(u.ID, f.ParentID)
	}
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]string{"folder_id": folderID, "status": "restored"})
}

func clearTrashJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
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
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]any{
		"deleted_jobs": len(toDelete),
		"status":       "cleared",
	})
}

func deleteTrashJobsJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
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
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]any{
		"deleted_jobs": len(toDelete),
		"job_ids":      toDelete,
	})
}
