package httpx

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

func RefineRetryHandler(c echo.Context, deps JobsDeps) error {
	jobID := c.Param("job_id")
	job, _, err := deps.RequireOwnedJob(c, jobID, false)
	if err != nil {
		return err
	}
	if job.Status != "완료" {
		return echo.NewHTTPError(http.StatusBadRequest, "작업이 완료된 후에만 정제를 시도할 수 있습니다.")
	}
	if !deps.HasGeminiConfigured() {
		return echo.NewHTTPError(http.StatusBadRequest, "정제 기능이 설정되어 있지 않습니다. (GEMINI_API_KEYS 필요)")
	}
	if !store.HasJobBlob(jobID, store.BlobKindTranscript) {
		return echo.NewHTTPError(http.StatusNotFound, "원본 전사 결과를 찾지 못했습니다.")
	}

	deps.SetJobFields(jobID, map[string]any{"status": "정제 대기 중"})
	deps.EnqueueRefine(jobID)
	deps.Logf("[REFINE_RETRY] queued job_id=%s", jobID)
	return c.Redirect(http.StatusSeeOther, routes.Job(jobID))
}
