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
	EnqueuePDFExtract      func(string)
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
	folderID := EnsureRestoredFolder(u.ID, deps.NormalizeFolderID(job.FolderID), deps.Logf, deps.Errf, "job.restore")
	updates := map[string]any{"is_trashed": false, "deleted_at": ""}
	updates["folder_id"] = folderID
	deps.SetJobFields(jobID, updates)
	ResumeRestoredJob(jobID, deps.GetJob(jobID), deps.HasAudioBlob, deps.HasJobBlob, deps.SetJobFields, deps.EnqueueTranscribe, deps.EnqueueRefine, deps.EnqueuePDFExtract, deps.StatusPending, deps.StatusRefiningPending)
	TouchFolderAncestors(u.ID, folderID, deps.Errf, "job.restoreTouchFolder", "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, folderID)
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
	deps.CancelJob(jobID)
	deps.SetJobFields(jobID, map[string]any{"is_trashed": true, "deleted_at": time.Now().Format("2006-01-02 15:04:05")})
	TouchFolderAncestors(u.ID, job.FolderID, deps.Errf, "job.trashTouchFolder", "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, job.FolderID)
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
		TouchFolderAncestors(u.ID, f.ParentID, deps.Errf, "folder.restoreTouchParent", "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
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
		TouchFolderAncestors(u.ID, f.ParentID, deps.Errf, "folder.trashTouchParent", "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
	}
	return c.Redirect(http.StatusSeeOther, routes.FilesHome)
}
