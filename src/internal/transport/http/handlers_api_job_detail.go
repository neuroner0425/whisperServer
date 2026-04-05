package httptransport

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/service"
)

type JobDetailHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	CurrentUserName           func(echo.Context) string
	GetJob                    func(string) *model.Job
	ToJobView                 func(*model.Job) any
	HasGeminiConfigured       func() bool
	TagSvc                    *service.TagService
	BlobSvc                   *service.JobBlobService

	StatusCompleted       string
	StatusRefiningPending string
	StatusRefining        string
}

func (h JobDetailHandlers) DetailJSON() echo.HandlerFunc {
	return func(c echo.Context) error {
		disableCache(c)
		if h.CurrentUserOrUnauthorized == nil || h.CurrentUserName == nil || h.GetJob == nil || h.ToJobView == nil || h.HasGeminiConfigured == nil || h.TagSvc == nil || h.BlobSvc == nil {
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

		payload := map[string]any{
			"job_id":               jobID,
			"current_user_name":    h.CurrentUserName(c),
			"job":                  h.ToJobView(job),
			"tag_text":             strings.Join(job.Tags, ", "),
			"selected_tags":        job.Tags,
			"status":               job.Status,
			"status_detail":        job.StatusDetail,
			"view":                 "waiting",
			"page_count":           job.PageCount,
			"processed_page_count": job.ProcessedPageCount,
			"current_chunk":        job.CurrentChunk,
			"total_chunks":         job.TotalChunks,
			"resume_available":     job.ResumeAvailable,
		}
		if tags, err := h.TagSvc.List(u.ID); err == nil {
			payload["available_tags"] = tags
		}
		payload["can_refine"] = h.HasGeminiConfigured() && job.FileType != "pdf"
		if h.BlobSvc.HasAudioAAC(jobID) {
			payload["audio_url"] = "/api/jobs/" + jobID + "/audio"
		}

		if job.Status == h.StatusCompleted {
			if job.FileType == "pdf" {
				if h.BlobSvc.HasDocumentMarkdown(jobID) {
					if b, err := h.BlobSvc.LoadDocumentMarkdown(jobID); err == nil {
						payload["view"] = "result"
						payload["text"] = string(b)
						payload["download_text_url"] = "/download/" + jobID
						payload["download_document_json_url"] = "/download/" + jobID + "/document-json"
						if h.BlobSvc.HasPDFOriginal(jobID) {
							payload["original_pdf_url"] = "/api/jobs/" + jobID + "/pdf"
						}
						payload["can_refine"] = false
					}
				}
				return c.JSON(http.StatusOK, payload)
			}

			showOriginal := strings.TrimSpace(c.QueryParam("original")) == "1" || strings.TrimSpace(c.QueryParam("original")) == "true"
			hasRefined := h.BlobSvc.HasRefined(jobID)
			useRefined := hasRefined && !showOriginal
			if useRefined {
				if h.BlobSvc.HasRefined(jobID) {
					if b, err := h.BlobSvc.LoadRefined(jobID); err == nil {
						payload["view"] = "result"
						payload["text"] = string(b)
						payload["has_refined"] = hasRefined
						payload["variant"] = map[bool]string{true: "original", false: "refined"}[!useRefined]
					}
				}
			} else {
				if h.BlobSvc.HasTranscript(jobID) {
					if b, err := h.BlobSvc.LoadTranscript(jobID); err == nil {
						payload["view"] = "result"
						payload["text"] = string(b)
						payload["has_refined"] = hasRefined
						payload["variant"] = map[bool]string{true: "original", false: "refined"}[!useRefined]
					}
				}
			}
			payload["download_url"] = routes.Job(jobID)
			payload["download_text_url"] = "/download/" + jobID
			payload["download_refined_url"] = "/download/" + jobID + "/refined"
			return c.JSON(http.StatusOK, payload)
		}

		if (job.Status == h.StatusRefiningPending || job.Status == h.StatusRefining) && h.BlobSvc.HasTranscript(jobID) {
			if b, err := h.BlobSvc.LoadTranscript(jobID); err == nil {
				payload["view"] = "preview"
				payload["original_text"] = string(b)
			}
		}
		payload["preview_text"] = job.PreviewText
		return c.JSON(http.StatusOK, payload)
	}
}

func (h JobDetailHandlers) Audio() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.GetJob == nil || h.BlobSvc == nil {
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

		audio, err := h.BlobSvc.LoadAudioAAC(jobID)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "오디오를 찾을 수 없습니다.")
		}
		res := c.Response()
		req := c.Request()
		res.Header().Set("Content-Disposition", `inline; filename="audio.m4a"`)
		res.Header().Set("Content-Type", "audio/mp4")
		res.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(res, req, "audio.m4a", time.Time{}, bytes.NewReader(audio))
		return nil
	}
}

func (h JobDetailHandlers) PDF() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.GetJob == nil || h.BlobSvc == nil {
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

		pdfBytes, err := h.BlobSvc.LoadPDFOriginal(jobID)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, "PDF를 찾을 수 없습니다.")
		}
		res := c.Response()
		req := c.Request()
		res.Header().Set("Content-Disposition", `inline; filename="document.pdf"`)
		res.Header().Set("Content-Type", "application/pdf")
		res.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(res, req, "document.pdf", time.Time{}, bytes.NewReader(pdfBytes))
		return nil
	}
}
