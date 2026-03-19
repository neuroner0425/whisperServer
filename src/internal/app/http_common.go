package app

import (
	"github.com/labstack/echo/v4"
	httpx "whisperserver/src/internal/http"
	"whisperserver/src/internal/model"
	intutil "whisperserver/src/internal/util"
)

func disableCache(c echo.Context) {
	httpx.DisableCache(c)
}

func rootRedirectHandler(c echo.Context) error {
	return httpx.RootRedirectHandler(func(c echo.Context) (*httpx.User, error) {
		return currentUser(c)
	})(c)
}

func redirectFilesToHomeHandler(c echo.Context) error {
	return httpx.RedirectFilesToHomeHandler(c)
}

func redirectJobsToRootHandler(c echo.Context) error {
	return httpx.RedirectJobsToRootHandler(c)
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
