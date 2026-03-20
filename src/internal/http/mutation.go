package httpx

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

type MutationDeps struct {
	CurrentUser            func(echo.Context) (*User, error)
	GetJob                 func(string) *model.Job
	SetJobFields           func(string, map[string]any)
	CancelJob              func(string)
	IsJobTrashed           func(*model.Job) bool
	CollectFolderSubtree   func(string, []string, bool) map[string]struct{}
	MarkSubtreeJobsTrashed func(string, map[string]struct{})
	Logf                   func(string, ...any)
	Errf                   func(string, error, string, ...any)
}

func BatchDeleteHandler(c echo.Context, deps MutationDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	if err := c.Request().ParseForm(); err != nil {
		deps.Errf("batchDelete.parseForm", err, "request parse failed")
		return c.Redirect(http.StatusSeeOther, routes.FilesHome)
	}
	jobIDs := c.Request().PostForm["job_ids"]
	folderIDs := c.Request().PostForm["folder_ids"]
	if len(jobIDs) == 0 && len(folderIDs) == 0 {
		deps.Logf("[BATCH_DELETE] skipped reason=no selection")
		return c.Redirect(http.StatusSeeOther, routes.FilesHome)
	}

	ownedJobs := make([]string, 0, len(jobIDs))
	touchedFolders := map[string]struct{}{}
	deletedAt := time.Now().Format("2006-01-02 15:04:05")
	for _, id := range jobIDs {
		job := deps.GetJob(id)
		if job != nil && job.OwnerID == u.ID && !deps.IsJobTrashed(job) {
			ownedJobs = append(ownedJobs, id)
			if job.FolderID != "" {
				touchedFolders[job.FolderID] = struct{}{}
			}
		}
	}
	for _, id := range ownedJobs {
		if deps.CancelJob != nil {
			deps.CancelJob(id)
		}
		deps.SetJobFields(id, map[string]any{"is_trashed": true, "deleted_at": deletedAt})
	}

	for _, id := range folderIDs {
		f, err := store.GetFolderByID(u.ID, id)
		if err == nil && f.ParentID != "" {
			touchedFolders[f.ParentID] = struct{}{}
		}
	}

	subtree := deps.CollectFolderSubtree(u.ID, folderIDs, true)
	deps.MarkSubtreeJobsTrashed(u.ID, subtree)
	for id := range touchedFolders {
		if err := store.TouchFolderAndAncestors(u.ID, id); err != nil {
			deps.Errf("batchDelete.touchFolder", err, "owner_id=%s folder_id=%s", u.ID, id)
		}
	}
	deps.Logf("[BATCH_TRASH] success jobs=%d folders=%d subtree=%d", len(ownedJobs), len(folderIDs), len(subtree))
	return c.Redirect(http.StatusSeeOther, routes.FilesHome)
}
