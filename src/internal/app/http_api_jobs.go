package app

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	httpx "whisperserver/src/internal/http"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

func apiJobDetailJSONHandler(c echo.Context) error {
	httpx.DisableCache(c)
	u, job, err := requireOwnedJobOrNotFound(c, false)
	if err != nil {
		return err
	}
	jobID := strings.TrimSpace(c.Param("job_id"))

	payload := map[string]any{
		"job_id":            jobID,
		"current_user_name": currentUserName(c),
		"job":               toJobView(job),
		"tag_text":          strings.Join(job.Tags, ", "),
		"selected_tags":     job.Tags,
		"status":            job.Status,
		"status_detail":     job.StatusDetail,
		"view":              "waiting",
	}
	if tags, err := store.ListTagsByOwner(u.ID); err == nil {
		payload["available_tags"] = tags
	}
	payload["can_refine"] = hasGeminiConfigured()
	if store.HasJobBlob(jobID, store.BlobKindAudioAAC) {
		payload["audio_url"] = "/api/jobs/" + jobID + "/audio"
	}

	if job.Status == statusCompleted {
		showOriginal := strings.TrimSpace(c.QueryParam("original")) == "1" || strings.TrimSpace(c.QueryParam("original")) == "true"
		hasRefined := store.HasJobBlob(jobID, store.BlobKindRefined)
		useRefined := hasRefined && !showOriginal
		blobKind := store.BlobKindTranscript
		if useRefined {
			blobKind = store.BlobKindRefined
		}
		if store.HasJobBlob(jobID, blobKind) {
			if b, err := store.LoadJobBlob(jobID, blobKind); err == nil {
				payload["view"] = "result"
				payload["text"] = string(b)
				payload["has_refined"] = hasRefined
				payload["variant"] = map[bool]string{true: "original", false: "refined"}[!useRefined]
			}
		}
		payload["download_url"] = routes.Job(jobID)
		payload["download_text_url"] = "/download/" + jobID
		payload["download_refined_url"] = "/download/" + jobID + "/refined"
		return c.JSON(http.StatusOK, payload)
	}

	if (job.Status == statusRefiningPending || job.Status == statusRefining) && store.HasJobBlob(jobID, store.BlobKindTranscript) {
		if b, err := store.LoadJobBlob(jobID, store.BlobKindTranscript); err == nil {
			payload["view"] = "preview"
			payload["original_text"] = string(b)
		}
	}
	payload["preview_text"] = job.PreviewText

	return c.JSON(http.StatusOK, payload)
}

func apiJobAudioHandler(c echo.Context) error {
	_, _, err := requireOwnedJobOrNotFound(c, false)
	if err != nil {
		return err
	}
	jobID := strings.TrimSpace(c.Param("job_id"))
	audio, err := store.LoadJobBlob(jobID, store.BlobKindAudioAAC)
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

func apiRetryJobJSONHandler(c echo.Context) error {
	_, job, err := requireOwnedJobOrNotFound(c, false)
	if err != nil {
		return err
	}
	jobID := strings.TrimSpace(c.Param("job_id"))
	if job.Status != statusFailed {
		return echo.NewHTTPError(http.StatusBadRequest, "실패한 작업만 재시도할 수 있습니다.")
	}
	if !store.HasJobBlob(jobID, store.BlobKindAudioAAC) {
		return echo.NewHTTPError(http.StatusBadRequest, "재시도할 오디오가 없습니다.")
	}

	resetJobForRetry(jobID, job.RefineEnabled)
	enqueueTranscribe(jobID)

	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "retried"})
}

func apiRetranscribeJobJSONHandler(c echo.Context) error {
	_, job, err := requireOwnedJobOrNotFound(c, false)
	if err != nil {
		return err
	}
	jobID := strings.TrimSpace(c.Param("job_id"))
	if job.Status != statusCompleted {
		return echo.NewHTTPError(http.StatusBadRequest, "완료된 작업만 전사를 다시 시작할 수 있습니다.")
	}
	if !store.HasJobBlob(jobID, store.BlobKindAudioAAC) {
		return echo.NewHTTPError(http.StatusBadRequest, "전사를 다시 시작할 오디오가 없습니다.")
	}

	shouldRefineAfterTranscribe := store.HasJobBlob(jobID, store.BlobKindRefined)
	resetJobForTranscribe(jobID, shouldRefineAfterTranscribe)
	enqueueTranscribe(jobID)

	return c.JSON(http.StatusOK, map[string]any{
		"job_id":      jobID,
		"status":      "retranscribing",
		"will_refine": shouldRefineAfterTranscribe,
	})
}

func apiRefineJobJSONHandler(c echo.Context) error {
	_, job, err := requireOwnedJobOrNotFound(c, false)
	if err != nil {
		return err
	}
	jobID := strings.TrimSpace(c.Param("job_id"))
	if job.Status != statusCompleted {
		return echo.NewHTTPError(http.StatusBadRequest, "전사 완료된 작업만 정제할 수 있습니다.")
	}
	if !hasGeminiConfigured() {
		return echo.NewHTTPError(http.StatusBadRequest, "정제 기능이 설정되어 있지 않습니다.")
	}
	if !store.HasJobBlob(jobID, store.BlobKindTranscript) {
		return echo.NewHTTPError(http.StatusNotFound, "원본 전사 결과를 찾지 못했습니다.")
	}
	if store.HasJobBlob(jobID, store.BlobKindRefined) {
		return echo.NewHTTPError(http.StatusBadRequest, "이미 정제된 작업입니다.")
	}

	setJobFields(jobID, map[string]any{
		"status":           statusRefiningPending,
		"progress_percent": 100,
		"progress_label":   "",
	})
	enqueueRefine(jobID)
	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "queued"})
}

func apiRerefineJobJSONHandler(c echo.Context) error {
	_, job, err := requireOwnedJobOrNotFound(c, false)
	if err != nil {
		return err
	}
	jobID := strings.TrimSpace(c.Param("job_id"))
	if job.Status != statusCompleted {
		return echo.NewHTTPError(http.StatusBadRequest, "완료된 작업만 정제를 다시 시작할 수 있습니다.")
	}
	if !hasGeminiConfigured() {
		return echo.NewHTTPError(http.StatusBadRequest, "정제 기능이 설정되어 있지 않습니다.")
	}
	if !store.HasJobBlob(jobID, store.BlobKindTranscript) {
		return echo.NewHTTPError(http.StatusNotFound, "원본 전사 결과를 찾지 못했습니다.")
	}
	if !store.HasJobBlob(jobID, store.BlobKindRefined) {
		return echo.NewHTTPError(http.StatusBadRequest, "정제 결과가 있는 작업만 다시 정제할 수 있습니다.")
	}

	store.DeleteJobBlob(jobID, store.BlobKindRefined)
	setJobFields(jobID, map[string]any{
		"result_refined":   "",
		"refine_enabled":   true,
		"status":           statusRefiningPending,
		"progress_percent": 100,
		"progress_label":   "",
		"status_detail":    "",
	})
	enqueueRefine(jobID)
	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "requeued"})
}
