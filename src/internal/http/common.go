package httpx

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
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

func CurrentUserOrUnauthorizedJSON(c echo.Context, currentUser func(echo.Context) (*User, error)) (*User, error) {
	u, err := currentUser(c)
	if err == nil {
		return u, nil
	}
	return nil, c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
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

func RequireFolderForOwner(ownerID, folderID string, allowTrashed bool, statusCode int, message string) (*model.Folder, error) {
	folderID = NormalizeFolderID(folderID)
	if folderID == "" {
		return nil, nil
	}
	folder, err := store.GetFolderByID(ownerID, folderID)
	if err != nil || (!allowTrashed && folder.IsTrashed) {
		return nil, echo.NewHTTPError(statusCode, message)
	}
	return folder, nil
}

func SelectedTagMap(tags []string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tags {
		out[t] = true
	}
	return out
}

func ValidateOwnedTags(ownerID string, tags []string) ([]string, error) {
	allowed, err := store.ListTagNamesByOwner(ownerID)
	if err != nil {
		return nil, err
	}
	validated := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if _, ok := allowed[tag]; ok {
			validated = append(validated, tag)
		}
	}
	return validated, nil
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

func TouchFolderAncestors(ownerID, folderID string, errf func(string, error, string, ...any), scope, details string, args ...any) {
	if err := store.TouchFolderAndAncestors(ownerID, folderID); err != nil && errf != nil {
		errf(scope, err, details, args...)
	}
}

func EnsureRestoredFolder(ownerID, folderID string, logf func(string, ...any), errf func(string, error, string, ...any), scopePrefix string) string {
	folderID = NormalizeFolderID(folderID)
	if folderID == "" {
		return ""
	}
	folder, err := store.GetFolderByID(ownerID, folderID)
	if err == nil {
		if folder.IsTrashed {
			if err := store.SetFolderTrashed(ownerID, folderID, false); err != nil && errf != nil {
				errf(scopePrefix+".restoreFolder", err, "owner_id=%s folder_id=%s", ownerID, folderID)
			}
		}
		return folderID
	}
	newID, err := store.CreateFolder(ownerID, "복구된 폴더", "")
	if err != nil {
		if errf != nil {
			errf(scopePrefix+".createFolder", err, "owner_id=%s missing_folder_id=%s", ownerID, folderID)
		}
		return ""
	}
	if logf != nil {
		logf("[RESTORE] created_folder owner_id=%s missing_folder_id=%s new_folder_id=%s", ownerID, folderID, newID)
	}
	return newID
}

func ResumeRestoredJob(jobID string, job *model.Job, hasAudio func(string) bool, hasBlob func(string, string) bool, setJobFields func(string, map[string]any), enqueueTranscribe func(string), enqueueRefine func(string), statusPending, statusRefiningPending string) {
	if job == nil || hasBlob == nil || setJobFields == nil {
		return
	}
	if hasAudio != nil && hasAudio(jobID) && !hasBlob(jobID, store.BlobKindTranscript) {
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
		if enqueueTranscribe != nil {
			enqueueTranscribe(jobID)
		}
		return
	}
	if hasBlob(jobID, store.BlobKindTranscript) && !hasBlob(jobID, store.BlobKindRefined) && job.RefineEnabled {
		setJobFields(jobID, map[string]any{
			"status":         statusRefiningPending,
			"progress_label": "",
			"completed_at":   "",
			"completed_ts":   0,
			"duration":       "",
			"status_detail":  "",
		})
		if enqueueRefine != nil {
			enqueueRefine(jobID)
		}
	}
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
