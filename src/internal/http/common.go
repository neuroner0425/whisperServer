package httpx

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
)

func DisableCache(c echo.Context) {
	c.Response().Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Expires", "0")
}

func RootRedirectHandler(currentUser func(echo.Context) (*User, error)) echo.HandlerFunc {
	return func(c echo.Context) error {
		_, err := currentUser(c)
		if err == nil {
			return c.Redirect(http.StatusSeeOther, routes.FilesHome)
		}
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
}

func RedirectFilesToHomeHandler(c echo.Context) error {
	return c.Redirect(http.StatusMovedPermanently, routes.FilesHome)
}

func RedirectJobsToRootHandler(c echo.Context) error {
	return c.Redirect(http.StatusMovedPermanently, routes.FilesHome)
}

func SafeReturnPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return routes.FilesHome
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return routes.FilesHome
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return routes.FilesHome
	}
	if u.Path == "" {
		u.Path = routes.FilesHome
	}
	return u.RequestURI()
}

func CurrentUserName(c echo.Context, currentUser func(echo.Context) (*User, error)) string {
	u, err := currentUser(c)
	if err != nil {
		return ""
	}
	if strings.TrimSpace(u.LoginID) != "" {
		return u.LoginID
	}
	if idx := strings.Index(u.Email, "@"); idx > 0 {
		return u.Email[:idx]
	}
	return u.Email
}

func RequireOwnedJob(c echo.Context, currentUser func(echo.Context) (*User, error), getJob func(string) *model.Job, jobID string, allowTrashed bool) (*model.Job, *User, error) {
	u, err := currentUser(c)
	if err != nil {
		return nil, nil, c.Redirect(http.StatusSeeOther, routes.Login)
	}
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID || (!allowTrashed && IsJobTrashed(job)) {
		return nil, u, echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	return job, u, nil
}

func SelectedTagMap(tags []string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tags {
		out[t] = true
	}
	return out
}

func ParseSelectedTags(c echo.Context, uniqueStrings func([]string) []string) []string {
	r := c.Request()
	if err := r.ParseMultipartForm(32 << 20); err == nil && r.MultipartForm != nil {
		return uniqueStrings(r.MultipartForm.Value["tags"])
	}
	if err := r.ParseForm(); err == nil {
		return uniqueStrings(r.Form["tags"])
	}
	return nil
}

func NormalizeFolderID(v string) string {
	return strings.TrimSpace(v)
}

func IsJobTrashed(job *model.Job) bool {
	return job != nil && job.IsTrashed
}

func ParsePositiveInt(s string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func NormalizeSortParams(sortBy, sortOrder string) (string, string) {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	sortOrder = strings.ToLower(strings.TrimSpace(sortOrder))
	if sortBy != "name" && sortBy != "updated" {
		sortBy = "updated"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		if sortBy == "name" {
			sortOrder = "asc"
		} else {
			sortOrder = "desc"
		}
	}
	return sortBy, sortOrder
}

func HealthzHandler(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
