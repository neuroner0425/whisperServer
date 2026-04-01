package app

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	httpx "whisperserver/src/internal/http"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/store"
)

func rootRedirectHandler(c echo.Context) error {
	if _, err := currentUser(c); err == nil {
		return c.Redirect(http.StatusSeeOther, "/files/home")
	}
	return c.Redirect(http.StatusSeeOther, "/auth/login")
}

func spaIndexHandler(c echo.Context) error {
	if _, err := os.Stat(spaIndexPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c.String(http.StatusServiceUnavailable, "SPA build not found. Run `npm install && npm run build` in ./frontend first.")
		}
		return c.String(http.StatusInternalServerError, "Failed to load SPA build.")
	}
	return c.File(spaIndexPath)
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

func currentUserOrUnauthorized(c echo.Context) (*AuthUser, error) {
	u, err := currentUser(c)
	if err == nil {
		return u, nil
	}
	_ = c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	return nil, err
}

func requireOwnedJobOrNotFound(c echo.Context, allowTrashed bool) (*AuthUser, *model.Job, error) {
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil, nil, err
	}
	jobID := strings.TrimSpace(c.Param("job_id"))
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID || (!allowTrashed && httpx.IsJobTrashed(job)) {
		return nil, nil, echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	return u, job, nil
}

func notifyFilesChanged(userID string) {
	eventBroker.Notify(userID, "files.changed", nil)
}

func resetJobForTranscribe(jobID string, refineEnabled bool) {
	cancelJob(jobID)
	removeTempWav(jobID)
	setJobFields(jobID, map[string]any{
		"result":           "",
		"result_refined":   "",
		"refine_enabled":   refineEnabled,
		"status":           statusPending,
		"phase":            "",
		"progress_percent": 0,
		"progress_label":   "",
		"preview_text":     "",
		"started_at":       "",
		"started_ts":       0,
		"completed_at":     "",
		"completed_ts":     0,
		"duration":         "",
		"status_detail":    "",
	})
	store.DeleteJobBlob(jobID, store.BlobKindPreview)
	store.DeleteJobBlob(jobID, store.BlobKindTranscript)
	store.DeleteJobBlob(jobID, store.BlobKindTranscriptJSON)
	store.DeleteJobBlob(jobID, store.BlobKindRefined)
}

func resetJobForRetry(jobID string, refineEnabled bool) {
	resetJobForTranscribe(jobID, refineEnabled)
}

func markJobTrashed(jobID string) {
	cancelJob(jobID)
	removeTempWav(jobID)
	setJobFields(jobID, map[string]any{
		"is_trashed": true,
		"deleted_at": time.Now().Format("2006-01-02 15:04:05"),
	})
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
