package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

// JobControlHandlers exposes retry and refine actions for existing jobs.
type JobControlHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	GetJob                    func(string) *model.Job
	SetJobFields              func(string, map[string]any)
	BlobSvc                   *service.JobBlobService

	HasJobBlob func(jobID, kind string) bool

	EnqueueTranscribe func(string)
	EnqueueRefine     func(string)
	EnqueuePDFExtract func(string)

	ResetForTranscribe  func(jobID string, refineEnabled bool)
	ResetForRefine      func(jobID string)
	ResetForPDF         func(jobID string)
	PrepareForPDFRetry  func(jobID string)
	HasGeminiConfigured func() bool

	StatusFailed          string
	StatusCompleted       string
	StatusRefiningPending string

	BlobAudioAAC    string
	BlobTranscript  string
	BlobPDFOriginal string
}

// Retry requeues a failed job using the artifact appropriate for its file type.
func (h JobControlHandlers) Retry() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.GetJob == nil || h.HasJobBlob == nil ||
			h.PrepareForPDFRetry == nil || h.ResetForTranscribe == nil ||
			h.EnqueuePDFExtract == nil || h.EnqueueTranscribe == nil {
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
		if !isRetryableFailureStatus(job.StatusCode, job.Status, h.StatusFailed) {
			return echo.NewHTTPError(http.StatusBadRequest, "실패한 작업만 재시도할 수 있습니다.")
		}

		if h.BlobSvc != nil && h.EnqueueRefine != nil && h.ResetForRefine != nil &&
			h.HasGeminiConfigured != nil && h.HasGeminiConfigured() &&
			h.BlobSvc.HasTranscriptJSON(jobID) &&
			(job.StatusCode == model.JobStatusRefineFailedCode ||
				(job.StatusCode == model.JobStatusFailedCode && job.FileType != "pdf")) {
			h.ResetForRefine(jobID)
			h.EnqueueRefine(jobID)
			return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "requeued_refine"})
		}

		// PDF jobs restart document extraction from the preserved original file.
		if job.FileType == "pdf" {
			if !h.HasJobBlob(jobID, h.BlobPDFOriginal) {
				return echo.NewHTTPError(http.StatusBadRequest, "재시도할 PDF가 없습니다.")
			}
			h.PrepareForPDFRetry(jobID)
			h.EnqueuePDFExtract(jobID)
			return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "retried"})
		}

		// Audio and video jobs restart transcription from the normalized AAC blob.
		if !h.HasJobBlob(jobID, h.BlobAudioAAC) {
			return echo.NewHTTPError(http.StatusBadRequest, "재시도할 오디오가 없습니다.")
		}
		h.ResetForTranscribe(jobID, job.RefineEnabled)
		h.EnqueueTranscribe(jobID)
		return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "retried"})
	}
}

func isRetryableFailureStatus(statusCode int, status string, failedLabel string) bool {
	switch statusCode {
	case model.JobStatusFailedCode,
		model.JobStatusAudioConvertFailedCode,
		model.JobStatusPDFConvertFailedCode,
		model.JobStatusTranscribeFailedCode,
		model.JobStatusRefineFailedCode,
		model.JobStatusPDFExtractFailedCode:
		return true
	}
	return strings.TrimSpace(status) == strings.TrimSpace(failedLabel)
}

// Retranscribe restarts a completed job from its source artifact.
func (h JobControlHandlers) Retranscribe() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.GetJob == nil || h.HasJobBlob == nil ||
			h.ResetForPDF == nil || h.ResetForTranscribe == nil ||
			h.EnqueuePDFExtract == nil || h.EnqueueTranscribe == nil {
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
		if job.Status != h.StatusCompleted {
			return echo.NewHTTPError(http.StatusBadRequest, "완료된 작업만 전사를 다시 시작할 수 있습니다.")
		}

		// PDF reprocessing rebuilds page extraction from the original upload.
		if job.FileType == "pdf" {
			if !h.HasJobBlob(jobID, h.BlobPDFOriginal) {
				return echo.NewHTTPError(http.StatusBadRequest, "문서를 다시 시작할 PDF가 없습니다.")
			}
			h.ResetForPDF(jobID)
			h.EnqueuePDFExtract(jobID)
			return c.JSON(http.StatusOK, map[string]any{
				"job_id":      jobID,
				"status":      "reprocessing",
				"will_refine": false,
			})
		}

		if !h.HasJobBlob(jobID, h.BlobAudioAAC) {
			return echo.NewHTTPError(http.StatusBadRequest, "전사를 다시 시작할 오디오가 없습니다.")
		}

		// If a refined result exists, queue refinement again after transcription finishes.
		shouldRefineAfterTranscribe := h.BlobSvc != nil && h.BlobSvc.HasRefined(jobID)
		h.ResetForTranscribe(jobID, shouldRefineAfterTranscribe)
		h.EnqueueTranscribe(jobID)
		return c.JSON(http.StatusOK, map[string]any{
			"job_id":      jobID,
			"status":      "retranscribing",
			"will_refine": shouldRefineAfterTranscribe,
		})
	}
}

// Refine schedules Gemini refinement for a completed transcript.
func (h JobControlHandlers) Refine() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.GetJob == nil || h.BlobSvc == nil ||
			h.SetJobFields == nil || h.EnqueueRefine == nil || h.HasGeminiConfigured == nil {
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
		if job.Status != h.StatusCompleted {
			return echo.NewHTTPError(http.StatusBadRequest, "전사 완료된 작업만 정제할 수 있습니다.")
		}
		if job.FileType == "pdf" {
			return echo.NewHTTPError(http.StatusBadRequest, "PDF 작업은 정제를 지원하지 않습니다.")
		}
		if !h.HasGeminiConfigured() {
			return echo.NewHTTPError(http.StatusBadRequest, "정제 기능이 설정되어 있지 않습니다.")
		}
		if !h.BlobSvc.HasTranscriptJSON(jobID) {
			return echo.NewHTTPError(http.StatusNotFound, "원본 전사 결과를 찾지 못했습니다.")
		}
		if h.BlobSvc.HasRefined(jobID) {
			return echo.NewHTTPError(http.StatusBadRequest, "이미 정제된 작업입니다.")
		}

		h.SetJobFields(jobID, map[string]any{
			"status":           h.StatusRefiningPending,
			"progress_percent": 100,
			"progress_label":   "",
		})
		h.EnqueueRefine(jobID)
		return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "queued"})
	}
}

// Rerefine discards the current refined blob and schedules refinement again.
func (h JobControlHandlers) Rerefine() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.GetJob == nil || h.BlobSvc == nil ||
			h.SetJobFields == nil || h.EnqueueRefine == nil || h.HasGeminiConfigured == nil {
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
		if job.Status != h.StatusCompleted {
			return echo.NewHTTPError(http.StatusBadRequest, "완료된 작업만 정제를 다시 시작할 수 있습니다.")
		}
		if job.FileType == "pdf" {
			return echo.NewHTTPError(http.StatusBadRequest, "PDF 작업은 정제를 지원하지 않습니다.")
		}
		if !h.HasGeminiConfigured() {
			return echo.NewHTTPError(http.StatusBadRequest, "정제 기능이 설정되어 있지 않습니다.")
		}
		if !h.BlobSvc.HasTranscriptJSON(jobID) {
			return echo.NewHTTPError(http.StatusNotFound, "원본 전사 결과를 찾지 못했습니다.")
		}
		if !h.BlobSvc.HasRefined(jobID) {
			return echo.NewHTTPError(http.StatusBadRequest, "정제 결과가 있는 작업만 다시 정제할 수 있습니다.")
		}

		h.BlobSvc.DeleteRefined(jobID)
		h.BlobSvc.DeleteRefinedTimeline(jobID)
		h.SetJobFields(jobID, map[string]any{
			"result_refined":   "",
			"refine_enabled":   true,
			"status":           h.StatusRefiningPending,
			"progress_percent": 100,
			"progress_label":   "",
			"status_detail":    "",
		})
		h.EnqueueRefine(jobID)
		return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "requeued"})
	}
}
