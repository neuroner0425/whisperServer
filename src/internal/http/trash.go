package httpx

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

type TrashDeps struct {
	CurrentUser            func(echo.Context) (*User, error)
	GetJob                 func(string) *model.Job
	SetJobFields           func(string, map[string]any)
	CancelJob              func(string)
	EnqueueTranscribe      func(string)
	EnqueueRefine          func(string)
	HasAudioBlob           func(string) bool
	HasJobBlob             func(string, string) bool
	StatusPending          string
	StatusRunning          string
	StatusRefiningPending  string
	StatusRefining         string
	IsJobTrashed           func(*model.Job) bool
	NormalizeFolderID      func(string) string
	CollectFolderSubtree   func(string, []string, bool) map[string]struct{}
	MarkSubtreeJobsTrashed func(string, map[string]struct{})
	Logf                   func(string, ...any)
	Errf                   func(string, error, string, ...any)
}

func RestoreJobHandler(c echo.Context, deps TrashDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	jobID := c.Param("job_id")
	job := deps.GetJob(jobID)
	if job == nil || job.OwnerID != u.ID {
		return c.Redirect(http.StatusSeeOther, routes.Trash)
	}
	folderID := deps.NormalizeFolderID(job.FolderID)
	updates := map[string]any{"is_trashed": false, "deleted_at": ""}
	if folderID != "" {
		f, ferr := store.GetFolderByID(u.ID, folderID)
		if ferr == nil {
			if f.IsTrashed {
				if err := store.SetFolderTrashed(u.ID, folderID, false); err != nil {
					deps.Errf("job.restoreFolder", err, "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, folderID)
				}
			}
		} else {
			newID, err := store.CreateFolder(u.ID, "복구된 폴더", "")
			if err != nil {
				deps.Errf("job.restoreCreateFolder", err, "owner_id=%s job_id=%s missing_folder_id=%s", u.ID, jobID, folderID)
				updates["folder_id"] = ""
			} else {
				updates["folder_id"] = newID
				deps.Logf("[JOB] restore created_folder owner_id=%s job_id=%s new_folder_id=%s", u.ID, jobID, newID)
			}
		}
	}
	deps.SetJobFields(jobID, updates)
	requeueRestoredJob(jobID, deps)
	restoreFolderID := folderID
	if nextFolderID, ok := updates["folder_id"].(string); ok {
		restoreFolderID = nextFolderID
	}
	if err := store.TouchFolderAndAncestors(u.ID, restoreFolderID); err != nil {
		deps.Errf("job.restoreTouchFolder", err, "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, restoreFolderID)
	}
	return c.Redirect(http.StatusSeeOther, routes.Trash)
}

func requeueRestoredJob(jobID string, deps TrashDeps) {
	job := deps.GetJob(jobID)
	if job == nil || deps.HasJobBlob == nil {
		return
	}
	if deps.HasAudioBlob != nil && deps.HasAudioBlob(jobID) && !deps.HasJobBlob(jobID, store.BlobKindTranscript) {
		deps.SetJobFields(jobID, map[string]any{
			"status":           deps.StatusPending,
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
		if deps.EnqueueTranscribe != nil {
			deps.EnqueueTranscribe(jobID)
		}
		return
	}
	if deps.HasJobBlob(jobID, store.BlobKindTranscript) && !deps.HasJobBlob(jobID, store.BlobKindRefined) && job.RefineEnabled {
		deps.SetJobFields(jobID, map[string]any{
			"status":         deps.StatusRefiningPending,
			"progress_label": "",
			"completed_at":   "",
			"completed_ts":   0,
			"duration":       "",
			"status_detail":  "",
		})
		if deps.EnqueueRefine != nil {
			deps.EnqueueRefine(jobID)
		}
	}
}

func TrashJobHandler(c echo.Context, deps TrashDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	jobID := c.Param("job_id")
	job := deps.GetJob(jobID)
	if job == nil || job.OwnerID != u.ID {
		return c.Redirect(http.StatusSeeOther, routes.FilesHome)
	}
	deps.CancelJob(jobID)
	deps.SetJobFields(jobID, map[string]any{"is_trashed": true, "deleted_at": time.Now().Format("2006-01-02 15:04:05")})
	if err := store.TouchFolderAndAncestors(u.ID, job.FolderID); err != nil {
		deps.Errf("job.trashTouchFolder", err, "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, job.FolderID)
	}
	return c.Redirect(http.StatusSeeOther, routes.FilesHome)
}

func RenameJobHandler(c echo.Context, deps TrashDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	jobID := c.Param("job_id")
	job := deps.GetJob(jobID)
	if job == nil || job.OwnerID != u.ID || deps.IsJobTrashed(job) {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	nextName := strings.TrimSpace(c.FormValue("new_name"))
	if nextName == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "새 파일명을 입력하세요.")
	}
	if strings.Contains(nextName, "/") || strings.Contains(nextName, `\`) {
		return echo.NewHTTPError(http.StatusBadRequest, "파일명에 경로 문자를 사용할 수 없습니다.")
	}
	deps.SetJobFields(jobID, map[string]any{"filename": nextName})
	deps.Logf("[JOB] rename owner_id=%s job_id=%s new_name=%s", u.ID, jobID, nextName)
	return c.Redirect(http.StatusSeeOther, routes.FilesHome)
}

func RestoreFolderHandler(c echo.Context, deps TrashDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	folderID := c.Param("folder_id")
	if err := store.SetFolderTrashed(u.ID, folderID, false); err != nil {
		deps.Errf("folder.restore", err, "owner_id=%s folder_id=%s", u.ID, folderID)
	}
	if f, err := store.GetFolderByID(u.ID, folderID); err == nil {
		if err := store.TouchFolderAndAncestors(u.ID, f.ParentID); err != nil {
			deps.Errf("folder.restoreTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
		}
	}
	return c.Redirect(http.StatusSeeOther, routes.Trash)
}

func TrashFolderHandler(c echo.Context, deps TrashDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	folderID := c.Param("folder_id")
	f, _ := store.GetFolderByID(u.ID, folderID)
	subtree := deps.CollectFolderSubtree(u.ID, []string{folderID}, false)
	if err := store.SetFolderTrashed(u.ID, folderID, true); err != nil {
		deps.Errf("folder.trash", err, "owner_id=%s folder_id=%s", u.ID, folderID)
	}
	deps.MarkSubtreeJobsTrashed(u.ID, subtree)
	if f != nil {
		if err := store.TouchFolderAndAncestors(u.ID, f.ParentID); err != nil {
			deps.Errf("folder.trashTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
		}
	}
	return c.Redirect(http.StatusSeeOther, routes.FilesHome)
}
