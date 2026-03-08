package app

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"html"
	htmpl "html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"golang.org/x/text/unicode/norm"
)

func homeHandler(c echo.Context) error {
	return c.Render(http.StatusOK, "home.html", nil)
}

func redirectJobsToRootHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	q := c.QueryParam("q")
	url := "/"
	if q != "" {
		url = "/?q=" + q
	}
	c.Set(ctxUserKey, u)
	return c.Redirect(http.StatusSeeOther, url)
}

func currentUserName(c echo.Context) string {
	u, err := currentUser(c)
	if err != nil {
		return ""
	}
	if strings.TrimSpace(u.LoginID) != "" {
		return u.LoginID
	}
	if idx := strings.Index(u.Email, "@"); idx > 0 {
		return u.Email[:idx]
	}
	return u.Email
}

func buildJobRowsForUser(userID, q string) []JobRow {
	qNorm := norm.NFC.String(strings.ToLower(q))
	jobsMu.RLock()
	rows := make([]JobRow, 0, len(jobs))
	for id, job := range jobs {
		if asString(job["owner_id"]) != userID {
			continue
		}
		filename := asString(job["filename"])
		if qNorm != "" && !strings.Contains(norm.NFC.String(strings.ToLower(filename)), qNorm) {
			continue
		}
		rows = append(rows, JobRow{
			ID:            id,
			Filename:      filename,
			MediaDuration: fallback(asString(job["media_duration"]), "-"),
			Status:        asString(job["status"]),
			IsRefined:     asString(job["result_refined"]) != "" && asString(job["status"]) == statusCompleted,
		})
	}
	jobsMu.RUnlock()
	sort.Slice(rows, func(i, j int) bool { return uploadedTS(rows[i].ID) > uploadedTS(rows[j].ID) })
	return rows
}

func parsePositiveInt(s string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v <= 0 {
		return def
	}
	return v
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

func uploadGetHandler(c echo.Context) error {
	return c.Render(http.StatusOK, "upload.html", map[string]any{
		"CurrentUserName": currentUserName(c),
	})
}

func uploadPostHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	procLogf("[UPLOAD] request received")
	fileHeader, err := c.FormFile("file")
	if err != nil {
		procErrf("upload.formFile", err, "missing file")
		return echo.NewHTTPError(http.StatusBadRequest, "파일이 없습니다.")
	}
	if fileHeader.Filename == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "파일을 선택하세요.")
	}
	if !allowedFile(fileHeader.Filename) {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("허용되지 않는 파일 형식입니다. 허용: %s", strings.Join(sortedExts(), ", ")))
	}
	if ct := fileHeader.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "audio/") && !strings.HasPrefix(ct, "video/") {
		return echo.NewHTTPError(http.StatusBadRequest, "오디오/비디오 파일만 업로드할 수 있습니다.")
	}

	inputName := c.FormValue("input_name")
	description := strings.TrimSpace(c.FormValue("description"))
	refineEnabled := truthy(c.FormValue("refine_enabled"))
	originalFilename := fileHeader.Filename
	ext := strings.ToLower(filepath.Ext(originalFilename))
	if inputName == "" {
		inputName = strings.TrimSuffix(originalFilename, filepath.Ext(originalFilename))
	}
	if !strings.HasSuffix(strings.ToLower(inputName), ext) {
		inputName += ext
	}

	safeName := secureFilename(originalFilename)
	jobID := uuid.NewString()
	tempPath := filepath.Join(tmpFolder, fmt.Sprintf("%s_temp%s", jobID, ext))
	wavPath := filepath.Join(tmpFolder, fmt.Sprintf("%s_%s.wav", jobID, safeName))

	totalBytes, err := saveUploadWithLimit(fileHeader, tempPath, int64(maxUploadSizeMB)*1024*1024)
	if err != nil {
		_ = os.Remove(tempPath)
		if errors.Is(err, errUploadTooLarge) {
			procErrf("upload.save", err, "file=%s too large", originalFilename)
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, fmt.Sprintf("업로드 용량 초과(%dMB)", maxUploadSizeMB))
		}
		procErrf("upload.save", err, "file=%s", originalFilename)
		return echo.NewHTTPError(http.StatusInternalServerError, "파일 저장 실패")
	}
	uploadBytes.Add(float64(totalBytes))

	if err := convertToWav(tempPath, wavPath); err != nil {
		_ = os.Remove(tempPath)
		procErrf("upload.convertToWav", err, "job_id=%s src=%s dst=%s", jobID, tempPath, wavPath)
		return echo.NewHTTPError(http.StatusInternalServerError, "ffmpeg 변환 실패")
	}
	_ = os.Remove(tempPath)

	wavBytes, err := os.ReadFile(wavPath)
	if err != nil {
		_ = os.Remove(wavPath)
		procErrf("upload.readWav", err, "job_id=%s path=%s", jobID, wavPath)
		return echo.NewHTTPError(http.StatusInternalServerError, "업로드 파일 처리 실패")
	}

	duration := getMediaDuration(wavPath)
	now := time.Now()
	job := map[string]any{
		"status":                 statusPending,
		"filename":               inputName,
		"result":                 nil,
		"uploaded_at":            now.Format("2006-01-02 15:04:05"),
		"uploaded_ts":            float64(now.Unix()),
		"duration":               duration,
		"media_duration":         formatSecondsPtr(duration),
		"media_duration_seconds": duration,
		"description":            nil,
		"refine_enabled":         refineEnabled,
		"owner_id":               u.ID,
	}
	if description != "" {
		job["description"] = description
	}

	jobsMu.Lock()
	jobs[jobID] = job
	saveJobsLocked()
	jobsMu.Unlock()

	if err := saveJobBlob(jobID, blobKindWav, wavBytes); err != nil {
		_ = os.Remove(wavPath)
		procErrf("upload.saveWavBlob", err, "job_id=%s", jobID)
		// rollback job metadata if blob persistence fails
		deleteJobs([]string{jobID})
		return echo.NewHTTPError(http.StatusInternalServerError, "업로드 파일 저장 실패")
	}
	_ = os.Remove(wavPath)

	enqueueTranscribe(jobID)
	procLogf("[UPLOAD] queued job_id=%s filename=%s bytes=%d", jobID, inputName, totalBytes)
	return c.Redirect(http.StatusSeeOther, "/job/"+jobID)
}

func jobsHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	q := c.QueryParam("q")
	page := parsePositiveInt(c.QueryParam("page"), 1)
	pageSize := parsePositiveInt(c.QueryParam("page_size"), 20)
	rows := buildJobRowsForUser(u.ID, q)
	pagedRows, page, totalPages := paginateRows(rows, page, pageSize)
	return c.Render(http.StatusOK, "jobs.html", map[string]any{
		"JobItems":        pagedRows,
		"SearchQuery":     q,
		"CurrentUserName": currentUserName(c),
		"Page":            page,
		"PageSize":        pageSize,
		"TotalPages":      totalPages,
	})
}

func jobsUpdatesHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	page := parsePositiveInt(c.QueryParam("page"), 1)
	pageSize := parsePositiveInt(c.QueryParam("page_size"), 20)
	rows := buildJobRowsForUser(u.ID, c.QueryParam("q"))
	pagedRows, page, totalPages := paginateRows(rows, page, pageSize)
	return c.JSON(http.StatusOK, map[string]any{
		"job_items":   pagedRows,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
		"total_items": len(rows),
	})
}

func statusHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	job := getJob(c.Param("job_id"))
	if job == nil {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	if asString(job["owner_id"]) != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	return c.JSON(http.StatusOK, map[string]any{
		"status":           fallback(asString(job["status"]), "알 수 없음"),
		"progress_percent": asInt(job["progress_percent"]),
		"phase":            fallback(asString(job["phase"]), "대기 중"),
		"progress_label":   asString(job["progress_label"]),
		"preview_text":     sanitizePreviewText(asString(job["preview_text"])),
	})
}

func jobHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	if asString(job["owner_id"]) != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	status := asString(job["status"])

	if status == statusRefiningPending || status == statusRefining {
		if hasJobBlob(jobID, blobKindTranscript) {
			b, err := loadJobBlob(jobID, blobKindTranscript)
			if err != nil {
				procErrf("job.loadTranscriptBlob", err, "job_id=%s", jobID)
				return echo.NewHTTPError(http.StatusInternalServerError, "원본 결과 읽기 실패")
			}
			esc := html.EscapeString(string(b))
			return c.Render(http.StatusOK, "preview.html", map[string]any{
				"Job":              toJobView(job),
				"JobID":            jobID,
				"OriginalTextHTML": htmpl.HTML(strings.ReplaceAll(esc, "\n", "<br>")),
				"CurrentUserName":  currentUserName(c),
			})
		}
		return c.Render(http.StatusOK, "waiting.html", map[string]any{"Job": toJobView(job), "JobID": jobID, "CurrentUserName": currentUserName(c)})
	}

	if status == statusCompleted {
		showOriginal := truthy(c.QueryParam("original"))
		hasRefined := hasJobBlob(jobID, blobKindRefined)
		useRefined := hasRefined && !showOriginal

		blobKind := blobKindTranscript
		if useRefined {
			blobKind = blobKindRefined
		}
		if !hasJobBlob(jobID, blobKind) {
			return echo.NewHTTPError(http.StatusNotFound, "결과 파일을 찾을 수 없습니다.")
		}
		b, err := loadJobBlob(jobID, blobKind)
		if err != nil {
			procErrf("job.loadResultBlob", err, "job_id=%s kind=%s", jobID, blobKind)
			return echo.NewHTTPError(http.StatusInternalServerError, "결과 읽기 실패")
		}
		return c.Render(http.StatusOK, "result.html", map[string]any{
			"Job":             toJobView(job),
			"JobID":           jobID,
			"Text":            renderResultText(string(b), !useRefined, asIntPtr(job["media_duration_seconds"])),
			"Variant":         map[bool]string{true: "original", false: "refined"}[!useRefined],
			"HasRefined":      hasRefined,
			"CanRefine":       hasGeminiConfigured(),
			"CurrentUserName": currentUserName(c),
		})
	}

	return c.Render(http.StatusOK, "waiting.html", map[string]any{"Job": toJobView(job), "JobID": jobID, "CurrentUserName": currentUserName(c)})
}

func downloadHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || asString(job["status"]) != statusCompleted {
		return echo.NewHTTPError(http.StatusNotFound, "다운로드할 결과가 없습니다.")
	}
	if asString(job["owner_id"]) != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "다운로드할 결과가 없습니다.")
	}
	b, err := loadJobBlob(jobID, blobKindTranscript)
	if err != nil {
		procErrf("download.loadTranscriptBlob", err, "job_id=%s", jobID)
		return echo.NewHTTPError(http.StatusNotFound, "다운로드할 결과가 없습니다.")
	}
	base := strings.TrimSuffix(asString(job["filename"]), filepath.Ext(asString(job["filename"])))
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, base+".txt"))
	return c.Blob(http.StatusOK, "text/plain; charset=utf-8", b)
}

func downloadRefinedHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || asString(job["status"]) != statusCompleted {
		return echo.NewHTTPError(http.StatusNotFound, "다운로드할 결과가 없습니다.")
	}
	if asString(job["owner_id"]) != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "정제본이 없습니다.")
	}
	b, err := loadJobBlob(jobID, blobKindRefined)
	if err != nil {
		procErrf("download.loadRefinedBlob", err, "job_id=%s", jobID)
		return echo.NewHTTPError(http.StatusNotFound, "정제본이 없습니다.")
	}
	base := strings.TrimSuffix(asString(job["filename"]), filepath.Ext(asString(job["filename"])))
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, base+"_refined.txt"))
	return c.Blob(http.StatusOK, "text/plain; charset=utf-8", b)
}

func batchDownloadHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	if err := c.Request().ParseForm(); err != nil {
		procErrf("batchDownload.parseForm", err, "request parse failed")
		return c.Redirect(http.StatusSeeOther, "/jobs")
	}
	ids := c.Request().PostForm["job_ids"]
	if len(ids) == 0 {
		procLogf("[BATCH_DOWNLOAD] skipped reason=no selection")
		return c.Redirect(http.StatusSeeOther, "/jobs")
	}

	buf := bytes.NewBuffer(nil)
	zw := zip.NewWriter(buf)
	added := 0

	for _, id := range ids {
		job := getJob(id)
		if job == nil || asString(job["status"]) != statusCompleted {
			continue
		}
		if asString(job["owner_id"]) != u.ID {
			continue
		}
		useRefined := hasJobBlob(id, blobKindRefined)
		blobKind := blobKindTranscript
		ext := ".txt"
		if useRefined {
			blobKind = blobKindRefined
			ext = "_refined.txt"
		}
		b, err := loadJobBlob(id, blobKind)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(asString(job["filename"]), filepath.Ext(asString(job["filename"])))
		w, err := zw.Create(base + ext)
		if err != nil {
			continue
		}
		if _, err := w.Write(b); err != nil {
			continue
		}
		added++
	}
	_ = zw.Close()

	if added == 0 {
		procLogf("[BATCH_DOWNLOAD] skipped reason=no downloadable results selected=%d", len(ids))
		return c.Redirect(http.StatusSeeOther, "/jobs")
	}
	procLogf("[BATCH_DOWNLOAD] success selected=%d added=%d", len(ids), added)
	zipName := "whisper_results_" + time.Now().Format("20060102_150405") + ".zip"
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, zipName))
	return c.Blob(http.StatusOK, "application/zip", buf.Bytes())
}

func batchDeleteHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	if err := c.Request().ParseForm(); err != nil {
		procErrf("batchDelete.parseForm", err, "request parse failed")
		return c.Redirect(http.StatusSeeOther, "/jobs")
	}
	ids := c.Request().PostForm["job_ids"]
	if len(ids) == 0 {
		procLogf("[BATCH_DELETE] skipped reason=no selection")
		return c.Redirect(http.StatusSeeOther, "/jobs")
	}
	owned := make([]string, 0, len(ids))
	for _, id := range ids {
		job := getJob(id)
		if job == nil {
			continue
		}
		if asString(job["owner_id"]) == u.ID {
			owned = append(owned, id)
		}
	}
	deleteJobs(owned)
	procLogf("[BATCH_DELETE] success count=%d", len(owned))
	return c.Redirect(http.StatusSeeOther, "/jobs")
}

func healthzHandler(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func refineRetryHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	if asString(job["owner_id"]) != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	if asString(job["status"]) != statusCompleted {
		return echo.NewHTTPError(http.StatusBadRequest, "작업이 완료된 후에만 정제를 시도할 수 있습니다.")
	}
	if !hasGeminiConfigured() {
		return echo.NewHTTPError(http.StatusBadRequest, "정제 기능이 설정되어 있지 않습니다. (GEMINI_API_KEYS 필요)")
	}
	if !hasJobBlob(jobID, blobKindTranscript) {
		return echo.NewHTTPError(http.StatusNotFound, "원본 전사 결과를 찾지 못했습니다.")
	}

	setJobFields(jobID, map[string]any{"status": statusRefiningPending})
	enqueueRefine(jobID)
	procLogf("[REFINE_RETRY] queued job_id=%s", jobID)
	return c.Redirect(http.StatusSeeOther, "/job/"+jobID)
}
