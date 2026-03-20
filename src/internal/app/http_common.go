package app

import (
	"net/http"

	"github.com/labstack/echo/v4"
	httpx "whisperserver/src/internal/http"
	"whisperserver/src/internal/model"
	intutil "whisperserver/src/internal/util"
)

func disableCache(c echo.Context) {
	httpx.DisableCache(c)
}

func rootRedirectHandler(c echo.Context) error {
	if _, err := currentUser(c); err == nil {
		return c.Redirect(http.StatusSeeOther, "/files/home")
	}
	return c.Redirect(http.StatusSeeOther, "/auth/login")
}

func spaLoginPageHandler(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/auth/login")
}

func spaSignupPageHandler(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/auth/join")
}

func redirectFilesToHomeHandler(c echo.Context) error {
	return c.Redirect(http.StatusMovedPermanently, "/files/home")
}

func spaFilesPageHandler(c echo.Context) error {
	return spaIndexHandler(c)
}

func legacyFilesPageRedirectHandler(c echo.Context) error {
	target := "/files/home"
	switch c.Path() {
	case "/files/root":
		target = "/files/root"
	case "/files/home":
		target = "/files/home"
	default:
		if folderID := c.Param("folder_id"); folderID != "" {
			target = "/files/folder/" + folderID
		}
	}
	if raw := c.QueryString(); raw != "" {
		target += "?" + raw
	}
	return c.Redirect(http.StatusSeeOther, target)
}

func redirectJobsToRootHandler(c echo.Context) error {
	return c.Redirect(http.StatusMovedPermanently, "/files/home")
}

func spaTagsPageHandler(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/files/home")
}

func spaTrashPageHandler(c echo.Context) error {
	return spaIndexHandler(c)
}

func spaUploadPageHandler(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/files/root")
}

func spaJobPageHandler(c echo.Context) error {
	target := "/file/" + c.Param("job_id")
	if raw := c.QueryString(); raw != "" {
		target += "?" + raw
	}
	return c.Redirect(http.StatusSeeOther, target)
}

func safeReturnPath(raw string) string {
	return httpx.SafeReturnPath(raw)
}

func currentUserName(c echo.Context) string {
	return httpx.CurrentUserName(c, func(c echo.Context) (*httpx.User, error) {
		return currentUser(c)
	})
}

func requireOwnedJob(c echo.Context, jobID string, allowTrashed bool) (*model.Job, *AuthUser, error) {
	return httpx.RequireOwnedJob(c, func(c echo.Context) (*httpx.User, error) {
		return currentUser(c)
	}, getJob, jobID, allowTrashed)
}

func selectedTagMap(tags []string) map[string]bool {
	return httpx.SelectedTagMap(tags)
}

func parseSelectedTags(c echo.Context) []string {
	return httpx.ParseSelectedTags(c, intutil.UniqueStringsKeepOrder)
}

func normalizeFolderID(v string) string {
	return httpx.NormalizeFolderID(v)
}

func isJobTrashed(job *model.Job) bool {
	return httpx.IsJobTrashed(job)
}

func parsePositiveInt(s string, def int) int {
	return httpx.ParsePositiveInt(s, def)
}

func paginateRows(rows []JobRow, page, pageSize int) ([]JobRow, int, int) {
	if pageSize <= 0 {
		pageSize = 20
	}
	totalPages := (len(rows) + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(rows) {
		start = len(rows)
	}
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], page, totalPages
}

func normalizeSortParams(sortBy, sortOrder string) (string, string) {
	return httpx.NormalizeSortParams(sortBy, sortOrder)
}

func healthzHandler(c echo.Context) error {
	return httpx.HealthzHandler(c)
}
