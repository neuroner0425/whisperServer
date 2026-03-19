package httpx

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
)

type MutationDeps struct {
	CurrentUser            func(echo.Context) (*User, error)
	GetJob                 func(string) *model.Job
	SetJobFields           func(string, map[string]any)
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
	for _, id := range jobIDs {
		job := deps.GetJob(id)
		if job != nil && job.OwnerID == u.ID && !deps.IsJobTrashed(job) {
			ownedJobs = append(ownedJobs, id)
		}
	}
	for _, id := range ownedJobs {
		deps.SetJobFields(id, map[string]any{"is_trashed": true})
	}

	subtree := deps.CollectFolderSubtree(u.ID, folderIDs, true)
	deps.MarkSubtreeJobsTrashed(u.ID, subtree)
	deps.Logf("[BATCH_TRASH] success jobs=%d folders=%d subtree=%d", len(ownedJobs), len(folderIDs), len(subtree))
	return c.Redirect(http.StatusSeeOther, routes.FilesHome)
}
