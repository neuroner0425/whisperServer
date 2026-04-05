package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/service"
)

type LegacyRefineHandlers struct {
	CurrentUser func(echo.Context) (*User, bool) // redirects happen in middleware already

	GetJob        func(string) *model.Job
	BlobSvc       *service.JobBlobService
	SetJobFields  func(string, map[string]any)
	EnqueueRefine func(string)

	HasGeminiConfigured func() bool

	StatusCompleted       string
	StatusRefiningPending string

	Logf func(string, ...any)
}

func (h LegacyRefineHandlers) RetryHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.GetJob == nil || h.BlobSvc == nil || h.SetJobFields == nil || h.EnqueueRefine == nil || h.HasGeminiConfigured == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			// Middleware should have handled redirects; keep behavior conservative.
			return c.Redirect(http.StatusSeeOther, routes.Login)
		}
		jobID := strings.TrimSpace(c.Param("job_id"))
		job := h.GetJob(jobID)
		if job == nil || job.OwnerID != u.ID || job.IsTrashed {
			return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
		}
		if strings.TrimSpace(h.StatusCompleted) != "" && job.Status != h.StatusCompleted {
			return echo.NewHTTPError(http.StatusBadRequest, "작업이 완료된 후에만 정제를 시도할 수 있습니다.")
		}
		if !h.HasGeminiConfigured() {
			return echo.NewHTTPError(http.StatusBadRequest, "정제 기능이 설정되어 있지 않습니다. (GEMINI_API_KEYS 필요)")
		}
		if !h.BlobSvc.HasTranscript(jobID) {
			return echo.NewHTTPError(http.StatusNotFound, "원본 전사 결과를 찾지 못했습니다.")
		}

		nextStatus := h.StatusRefiningPending
		if strings.TrimSpace(nextStatus) == "" {
			nextStatus = "정제 대기 중"
		}
		h.SetJobFields(jobID, map[string]any{"status": nextStatus})
		h.EnqueueRefine(jobID)
		if h.Logf != nil {
			h.Logf("[REFINE_RETRY] queued job_id=%s", jobID)
		}
		return c.Redirect(http.StatusSeeOther, routes.Job(jobID))
	}
}
