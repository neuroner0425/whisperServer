package app

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

func apiJobDetailJSONHandler(c echo.Context) error {
	disableCache(c)
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}

	jobID := strings.TrimSpace(c.Param("job_id"))
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID || isJobTrashed(job) {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}

	payload := map[string]any{
		"job_id":            jobID,
		"current_user_name": currentUserName(c),
		"job":               toJobView(job),
		"tag_text":          strings.Join(job.Tags, ", "),
		"selected_tags":     job.Tags,
		"status":            job.Status,
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
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	jobID := strings.TrimSpace(c.Param("job_id"))
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID || isJobTrashed(job) {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
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
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}

	jobID := strings.TrimSpace(c.Param("job_id"))
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID || isJobTrashed(job) {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	if job.Status != statusFailed {
		return echo.NewHTTPError(http.StatusBadRequest, "실패한 작업만 재시도할 수 있습니다.")
	}
	if !store.HasJobBlob(jobID, store.BlobKindAudioAAC) {
		return echo.NewHTTPError(http.StatusBadRequest, "재시도할 오디오가 없습니다.")
	}

	setJobFields(jobID, map[string]any{
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
	store.DeleteJobBlob(jobID, store.BlobKindTranscript)
	store.DeleteJobBlob(jobID, store.BlobKindTranscriptJSON)
	store.DeleteJobBlob(jobID, store.BlobKindRefined)
	enqueueTranscribe(jobID)

	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "retried"})
}

func apiRefineJobJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}

	jobID := strings.TrimSpace(c.Param("job_id"))
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID || isJobTrashed(job) {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
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

func apiUpdateJobTagsJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	allowed, err := store.ListTagNamesByOwner(u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	validated := make([]string, 0, len(body.Tags))
	for _, t := range body.Tags {
		t = strings.TrimSpace(t)
		if _, ok := allowed[t]; ok {
			validated = append(validated, t)
		}
	}
	setJobFields(jobID, map[string]any{"tags": validated})
	return c.JSON(http.StatusOK, map[string]any{"job_id": jobID, "tags": validated})
}
