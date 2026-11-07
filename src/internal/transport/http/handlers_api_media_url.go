package httptransport

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
)

// MediaURLHandlers provides direct presigned GET URLs for streaming media playback.
type MediaURLHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	GetJob                    func(jobID string) *model.Job
	Storage                   storage.Storage
	Logf                      func(string, ...any)
	Errf                      func(string, error, string, ...any)
}

// Get handles GET /api/jobs/:job_id/media-url?kind=audio|pdf.
func (h MediaURLHandlers) Get() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Auth handler unavailable")
		}

		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}

		jobID := strings.TrimSpace(c.Param("job_id"))
		if jobID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "Missing job_id parameter")
		}

		if h.GetJob == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Job store unavailable")
		}

		job := h.GetJob(jobID)
		if job == nil || job.IsTrashed {
			return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
		}

		// R-18: Strict owner isolation check
		if job.OwnerID != u.ID {
			return echo.NewHTTPError(http.StatusForbidden, "해당 미디어에 대한 접근 권한이 없습니다.")
		}

		// Security: Malware quarantined files cannot be streamed or downloaded
		if job.StatusCode == model.JobStatusQuarantinedInfectedCode {
			return echo.NewHTTPError(http.StatusForbidden, "악성코드가 검출되어 차단된 미디어입니다.")
		}

		if h.Storage == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Storage service unavailable")
		}

		kind := strings.ToLower(strings.TrimSpace(c.QueryParam("kind")))
		if kind == "" {
			if job.FileType == "pdf" {
				kind = "pdf"
			} else {
				kind = "audio"
			}
		}

		var targetKey string
		switch kind {
		case "audio":
			targetKey = fmt.Sprintf("jobs/%s/audio_aac", jobID)
		case "pdf":
			targetKey = fmt.Sprintf("jobs/%s/pdf_original", jobID)
		default:
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid kind (supported: audio, pdf)")
		}

		ctx := c.Request().Context()
		exists, err := h.Storage.Exists(ctx, targetKey)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to inspect storage")
		}
		if !exists {
			// Fallback check: source_raw if before normalize/render or if key differs
			sourceKey := fmt.Sprintf("jobs/%s/source_raw", jobID)
			srcExists, _ := h.Storage.Exists(ctx, sourceKey)
			if srcExists && kind == "pdf" {
				targetKey = sourceKey
			} else {
				return echo.NewHTTPError(http.StatusNotFound, "미디어 파일이 아직 준비되지 않았거나 존재하지 않습니다.")
			}
		}

		expires := 10 * time.Minute
		downloadFilename := job.Filename
		mediaURL, err := h.Storage.PresignGet(ctx, targetKey, downloadFilename, expires)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to generate presigned media URL: %v", err))
		}

		return c.JSON(http.StatusOK, map[string]any{
			"job_id":     jobID,
			"media_url":  mediaURL,
			"kind":       kind,
			"expires_in": int(expires.Seconds()),
		})
	}
}
