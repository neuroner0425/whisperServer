package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

// JobMutationHandlers serves rename and trash actions for a single job.
type JobMutationHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	FolderSvc                 *service.FolderService
	GetJob                    func(string) *model.Job
	SetJobFields              func(string, map[string]any)
	MarkJobTrashed            func(string)
	Errf                      func(scope string, err error, format string, args ...any)
}

// Rename updates the display filename shown to the user.
func (h JobMutationHandlers) Rename() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.GetJob == nil || h.SetJobFields == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		jobID := strings.TrimSpace(c.Param("job_id"))
		job := h.GetJob(jobID)
		if job == nil || job.OwnerID != u.ID || job.IsTrashed {
			return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
		}

		var body struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
		}
		nextName := strings.TrimSpace(body.Name)
		if nextName == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "새 파일명을 입력하세요.")
		}
		if strings.Contains(nextName, "/") || strings.Contains(nextName, `\`) {
			return echo.NewHTTPError(http.StatusBadRequest, "파일명에 경로 문자를 사용할 수 없습니다.")
		}
		h.SetJobFields(jobID, map[string]any{"filename": nextName})
		return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "name": nextName})
	}
}

// Trash moves the job into the trash view without deleting artifacts.
func (h JobMutationHandlers) Trash() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.FolderSvc == nil || h.GetJob == nil || h.MarkJobTrashed == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		jobID := strings.TrimSpace(c.Param("job_id"))
		job := h.GetJob(jobID)
		if job == nil || job.OwnerID != u.ID {
			return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
		}
		h.MarkJobTrashed(jobID)
		if err := h.FolderSvc.TouchAncestors(u.ID, job.FolderID); err != nil && h.Errf != nil {
			h.Errf("api.job.trashTouchFolder", err, "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, job.FolderID)
		}
		return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "trashed"})
	}
}
