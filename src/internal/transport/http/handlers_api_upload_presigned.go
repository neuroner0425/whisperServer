package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/service"
)

// PresignedUploadHandlers manages presigned direct upload initiation and completion.
type PresignedUploadHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	UploadSvc                 *service.UploadService
	NormalizeFolderID         func(string) string
}

// Initiate handles POST /api/uploads/initiate.
func (h PresignedUploadHandlers) Initiate() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.UploadSvc == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Upload service unavailable")
		}

		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}

		var req struct {
			Filename    string `json:"filename"`
			Size        int64  `json:"size"`
			ContentType string `json:"content_type"`
			FolderID    string `json:"folder_id"`
			Language    string `json:"language"`
		}
		if err := c.Bind(&req); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid request body")
		}

		folderID := strings.TrimSpace(req.FolderID)
		if h.NormalizeFolderID != nil {
			folderID = h.NormalizeFolderID(folderID)
		}

		res, err := h.UploadSvc.InitiatePresigned(c.Request().Context(), service.UploadInitiateRequest{
			OwnerID:     u.ID,
			Filename:    req.Filename,
			Size:        req.Size,
			ContentType: req.ContentType,
			FolderID:    folderID,
			Language:    req.Language,
		})
		if err != nil {
			if he, ok := err.(*service.HTTPError); ok {
				return echo.NewHTTPError(he.Status, he.Message)
			}
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		return c.JSON(http.StatusOK, res)
	}
}

// Complete handles POST /api/uploads/:job_id/complete.
func (h PresignedUploadHandlers) Complete() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.UploadSvc == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Upload service unavailable")
		}

		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}

		jobID := strings.TrimSpace(c.Param("job_id"))
		if jobID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "Missing job_id parameter")
		}

		var req struct {
			Filename    string   `json:"filename"`
			FolderID    string   `json:"folder_id"`
			Language    string   `json:"language"`
			Tags        []string `json:"tags"`
			Description string   `json:"description"`
		}
		_ = c.Bind(&req)

		folderID := strings.TrimSpace(req.FolderID)
		if h.NormalizeFolderID != nil {
			folderID = h.NormalizeFolderID(folderID)
		}

		job, err := h.UploadSvc.CompletePresigned(c.Request().Context(), service.UploadCompleteRequest{
			OwnerID:     u.ID,
			JobID:       jobID,
			Filename:    req.Filename,
			FolderID:    folderID,
			Language:    req.Language,
			Tags:        req.Tags,
			Description: req.Description,
		})
		if err != nil {
			if he, ok := err.(*service.HTTPError); ok {
				return echo.NewHTTPError(he.Status, he.Message)
			}
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		return c.JSON(http.StatusOK, map[string]any{
			"job_id":      jobID,
			"filename":    job.Filename,
			"file_type":   job.FileType,
			"status_code": job.StatusCode,
			"status":      job.Status,
			"uploaded_at": job.UploadedAt,
		})
	}
}
