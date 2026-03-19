package httpx

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

type TrashDeps struct {
	CurrentUser            func(echo.Context) (*User, error)
	GetJob                 func(string) *model.Job
	SetJobFields           func(string, map[string]any)
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
	updates := map[string]any{"is_trashed": false}
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
	return c.Redirect(http.StatusSeeOther, routes.Trash)
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
	deps.SetJobFields(jobID, map[string]any{"is_trashed": true})
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
	return c.Redirect(http.StatusSeeOther, routes.Trash)
}

func TrashFolderHandler(c echo.Context, deps TrashDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	folderID := c.Param("folder_id")
	subtree := deps.CollectFolderSubtree(u.ID, []string{folderID}, false)
	if err := store.SetFolderTrashed(u.ID, folderID, true); err != nil {
		deps.Errf("folder.trash", err, "owner_id=%s folder_id=%s", u.ID, folderID)
	}
	deps.MarkSubtreeJobsTrashed(u.ID, subtree)
	return c.Redirect(http.StatusSeeOther, routes.FilesHome)
}
