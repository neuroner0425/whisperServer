package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"html"
	htmpl "html/template"
	"net/http"
	"net/url"
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
	tag := c.QueryParam("tag")
	folder := c.QueryParam("folder")
	url := "/"
	values := make([]string, 0, 2)
	if q != "" {
		values = append(values, "q="+q)
	}
	if tag != "" {
		values = append(values, "tag="+tag)
	}
	if folder != "" {
		values = append(values, "folder="+folder)
	}
	if len(values) > 0 {
		url = "/?" + strings.Join(values, "&")
	}
	c.Set(ctxUserKey, u)
	return c.Redirect(http.StatusSeeOther, url)
}

func safeReturnPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}
	if strings.ContainsAny(raw, "\r\n") {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "/"
	}
	if u.IsAbs() || u.Host != "" {
		return "/"
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.RequestURI()
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

func selectedTagMap(tags []string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tags {
		out[t] = true
	}
	return out
}

func parseSelectedTags(c echo.Context) []string {
	r := c.Request()
	if err := r.ParseMultipartForm(32 << 20); err == nil && r.MultipartForm != nil {
		return uniqueStringsKeepOrder(r.MultipartForm.Value["tags"])
	}
	if err := r.ParseForm(); err == nil {
		return uniqueStringsKeepOrder(r.Form["tags"])
	}
	return nil
}

func normalizeFolderID(v string) string {
	return strings.TrimSpace(v)
}

func isJobTrashed(job map[string]any) bool {
	return asBool(job["is_trashed"]) || truthy(asString(job["is_trashed"]))
}

func buildJobRowsForUser(userID, q, tag, folderID string, trashed bool) []JobRow {
	qNorm := norm.NFC.String(strings.ToLower(q))
	tag = strings.TrimSpace(tag)
	folderID = normalizeFolderID(folderID)
	jobsMu.RLock()
	rows := make([]JobRow, 0, len(jobs))
	for id, job := range jobs {
		if asString(job["owner_id"]) != userID {
			continue
		}
		if isJobTrashed(job) != trashed {
			continue
		}
		if !trashed {
			if normalizeFolderID(asString(job["folder_id"])) != folderID {
				continue
			}
		}
		filename := asString(job["filename"])
		if qNorm != "" && !strings.Contains(norm.NFC.String(strings.ToLower(filename)), qNorm) {
			continue
		}
		tags := asStringSlice(job["tags"])
		if tag != "" {
			found := false
			for _, t := range tags {
				if t == tag {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		rows = append(rows, JobRow{
			ID:            id,
			Filename:      filename,
			MediaDuration: fallback(asString(job["media_duration"]), "-"),
			Status:        asString(job["status"]),
			IsRefined:     asString(job["result_refined"]) != "" && asString(job["status"]) == statusCompleted,
			TagText:       strings.Join(tags, ", "),
			FolderID:      normalizeFolderID(asString(job["folder_id"])),
			IsTrashed:     isJobTrashed(job),
		})
	}
	jobsMu.RUnlock()
	sort.Slice(rows, func(i, j int) bool { return uploadedTS(rows[i].ID) > uploadedTS(rows[j].ID) })
	return rows
}

func buildFolderRowsForUser(userID, folderID, q string) []FolderRow {
	folderID = normalizeFolderID(folderID)
	folders, err := listFoldersByParent(userID, folderID, false)
	if err != nil {
		procErrf("folders.listByParent", err, "owner_id=%s folder_id=%s", userID, folderID)
		return nil
	}
	qNorm := norm.NFC.String(strings.ToLower(strings.TrimSpace(q)))
	out := make([]FolderRow, 0, len(folders))
	for _, f := range folders {
		if qNorm != "" && !strings.Contains(norm.NFC.String(strings.ToLower(f.Name)), qNorm) {
			continue
		}
		out = append(out, FolderRow{ID: f.ID, Name: f.Name, ParentID: f.ParentID})
	}
	return out
}

func buildRecentJobRowsForUser(userID, q, tag string) []JobRow {
	qNorm := norm.NFC.String(strings.ToLower(strings.TrimSpace(q)))
	tag = strings.TrimSpace(tag)
	jobsMu.RLock()
	rows := make([]JobRow, 0, len(jobs))
	for id, job := range jobs {
		if asString(job["owner_id"]) != userID || isJobTrashed(job) {
			continue
		}
		filename := asString(job["filename"])
		if qNorm != "" && !strings.Contains(norm.NFC.String(strings.ToLower(filename)), qNorm) {
			continue
		}
		tags := asStringSlice(job["tags"])
		if tag != "" {
			found := false
			for _, t := range tags {
				if t == tag {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		rows = append(rows, JobRow{
			ID:            id,
			Filename:      filename,
			MediaDuration: fallback(asString(job["media_duration"]), "-"),
			Status:        asString(job["status"]),
			IsRefined:     asString(job["result_refined"]) != "" && asString(job["status"]) == statusCompleted,
			TagText:       strings.Join(tags, ", "),
			FolderID:      normalizeFolderID(asString(job["folder_id"])),
			IsTrashed:     false,
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

func normalizeSortParams(sortBy, sortOrder string) (string, string) {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	sortOrder = strings.ToLower(strings.TrimSpace(sortOrder))
	if sortBy != "name" && sortBy != "updated" {
		sortBy = "updated"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		if sortBy == "name" {
			sortOrder = "asc"
		} else {
			sortOrder = "desc"
		}
	}
	return sortBy, sortOrder
}

func sortJobRows(rows []JobRow, sortBy, sortOrder string) {
	desc := sortOrder == "desc"
	switch sortBy {
	case "name":
		sort.Slice(rows, func(i, j int) bool {
			a := strings.ToLower(rows[i].Filename)
			b := strings.ToLower(rows[j].Filename)
			if a == b {
				if desc {
					return uploadedTS(rows[i].ID) > uploadedTS(rows[j].ID)
				}
				return uploadedTS(rows[i].ID) < uploadedTS(rows[j].ID)
			}
			if desc {
				return a > b
			}
			return a < b
		})
	default:
		sort.Slice(rows, func(i, j int) bool {
			if desc {
				return uploadedTS(rows[i].ID) > uploadedTS(rows[j].ID)
			}
			return uploadedTS(rows[i].ID) < uploadedTS(rows[j].ID)
		})
	}
}

func sortFolderRows(rows []FolderRow, sortOrder string) {
	desc := sortOrder == "desc"
	sort.Slice(rows, func(i, j int) bool {
		a := strings.ToLower(rows[i].Name)
		b := strings.ToLower(rows[j].Name)
		if a == b {
			if desc {
				return rows[i].ID > rows[j].ID
			}
			return rows[i].ID < rows[j].ID
		}
		if desc {
			return a > b
		}
		return a < b
	})
}

func jobsSnapshotVersion(jobItems []JobRow, folderItems []FolderRow, page, pageSize, totalPages, totalItems int) string {
	h := fnv.New64a()
	fmt.Fprintf(h, "p=%d|ps=%d|tp=%d|ti=%d;", page, pageSize, totalPages, totalItems)
	for _, f := range folderItems {
		fmt.Fprintf(h, "F|%s|%s|%s;", f.ID, f.Name, f.ParentID)
	}
	for _, j := range jobItems {
		fmt.Fprintf(
			h,
			"J|%s|%s|%s|%s|%t|%s|%s|%t;",
			j.ID,
			j.Filename,
			j.MediaDuration,
			j.Status,
			j.IsRefined,
			j.TagText,
			j.FolderID,
			j.IsTrashed,
		)
	}
	return fmt.Sprintf("%x", h.Sum64())
}

func uploadGetHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	tags, err := listTagsByOwner(u.ID)
	if err != nil {
		procErrf("upload.listTags", err, "owner_id=%s", u.ID)
	}
	folders, err := listAllFoldersByOwner(u.ID, false)
	if err != nil {
		procErrf("upload.listFolders", err, "owner_id=%s", u.ID)
	}
	return c.Render(http.StatusOK, "upload.html", map[string]any{
		"CurrentUserName": currentUserName(c),
		"Tags":            tags,
		"Folders":         folders,
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
	selectedTags := parseSelectedTags(c)
	folderID := normalizeFolderID(c.FormValue("folder_id"))
	allowedTags, err := listTagNamesByOwner(u.ID)
	if err != nil {
		procErrf("upload.listTagNames", err, "owner_id=%s", u.ID)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	validatedTags := make([]string, 0, len(selectedTags))
	for _, t := range selectedTags {
		if _, ok := allowedTags[t]; ok {
			validatedTags = append(validatedTags, t)
		}
	}
	refineEnabled := truthy(c.FormValue("refine_enabled"))
	if folderID != "" {
		f, err := getFolderByID(u.ID, folderID)
		if err != nil || f.IsTrashed {
			return echo.NewHTTPError(http.StatusBadRequest, "유효하지 않은 폴더입니다.")
		}
	}
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
		"tags":                   validatedTags,
		"folder_id":              folderID,
		"is_trashed":             false,
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
	tag := c.QueryParam("tag")
	folderID := normalizeFolderID(c.QueryParam("folder"))
	sortBy, sortOrder := normalizeSortParams(c.QueryParam("sort"), c.QueryParam("order"))
	view := strings.TrimSpace(c.QueryParam("view"))
	if view == "" {
		view = "explore"
	}
	page := parsePositiveInt(c.QueryParam("page"), 1)
	pageSize := parsePositiveInt(c.QueryParam("page_size"), 20)
	if view == "explore" && folderID != "" {
		f, err := getFolderByID(u.ID, folderID)
		if err != nil || f.IsTrashed {
			return echo.NewHTTPError(http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		}
	}
	rows := buildRecentJobRowsForUser(u.ID, q, tag)
	folderItems := []FolderRow{}
	if view == "explore" {
		rows = buildJobRowsForUser(u.ID, q, tag, folderID, false)
		folderItems = buildFolderRowsForUser(u.ID, folderID, q)
		sortFolderRows(folderItems, sortOrder)
	}
	sortJobRows(rows, sortBy, sortOrder)
	pagedRows, page, totalPages := paginateRows(rows, page, pageSize)
	snapshotVersion := jobsSnapshotVersion(pagedRows, folderItems, page, pageSize, totalPages, len(rows))
	tags, err := listTagsByOwner(u.ID)
	if err != nil {
		procErrf("jobs.listTags", err, "owner_id=%s", u.ID)
	}
	folders, err := listFoldersByParent(u.ID, folderID, false)
	if err != nil {
		procErrf("jobs.listFoldersByParent", err, "owner_id=%s folder=%s", u.ID, folderID)
	}
	allFolders, err := listAllFoldersByOwner(u.ID, false)
	if err != nil {
		procErrf("jobs.listAllFolders", err, "owner_id=%s", u.ID)
	}
	allFoldersJSON, _ := json.Marshal(allFolders)
	path, err := listFolderPath(u.ID, folderID)
	if err != nil {
		procErrf("jobs.listFolderPath", err, "owner_id=%s folder=%s", u.ID, folderID)
	}
	return c.Render(http.StatusOK, "jobs.html", map[string]any{
		"JobItems":        pagedRows,
		"SearchQuery":     q,
		"SelectedTag":     tag,
		"Tags":            tags,
		"Folders":         folders,
		"FolderItems":     folderItems,
		"CurrentFolderID": folderID,
		"FolderPath":      path,
		"AllFolders":      allFolders,
		"AllFoldersJSON":  string(allFoldersJSON),
		"ViewMode":        view,
		"CurrentUserName": currentUserName(c),
		"Page":            page,
		"PageSize":        pageSize,
		"TotalPages":      totalPages,
		"SnapshotVersion": snapshotVersion,
		"SelectedSort":    sortBy,
		"SelectedOrder":   sortOrder,
	})
}

func jobsUpdatesHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	page := parsePositiveInt(c.QueryParam("page"), 1)
	pageSize := parsePositiveInt(c.QueryParam("page_size"), 20)
	q := c.QueryParam("q")
	tag := c.QueryParam("tag")
	folderID := c.QueryParam("folder")
	sortBy, sortOrder := normalizeSortParams(c.QueryParam("sort"), c.QueryParam("order"))
	view := strings.TrimSpace(c.QueryParam("view"))
	if view == "" {
		view = "explore"
	}
	rows := buildRecentJobRowsForUser(u.ID, q, tag)
	folderItems := []FolderRow{}
	if view == "explore" {
		rows = buildJobRowsForUser(u.ID, q, tag, folderID, false)
		folderItems = buildFolderRowsForUser(u.ID, folderID, q)
		sortFolderRows(folderItems, sortOrder)
	}
	sortJobRows(rows, sortBy, sortOrder)
	pagedRows, page, totalPages := paginateRows(rows, page, pageSize)
	snapshotVersion := jobsSnapshotVersion(pagedRows, folderItems, page, pageSize, totalPages, len(rows))
	clientVersion := strings.TrimSpace(c.QueryParam("v"))
	if clientVersion != "" && clientVersion == snapshotVersion {
		return c.JSON(http.StatusOK, map[string]any{
			"changed":     false,
			"version":     snapshotVersion,
			"page":        page,
			"page_size":   pageSize,
			"total_pages": totalPages,
			"total_items": len(rows),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"changed":      true,
		"version":      snapshotVersion,
		"job_items":    pagedRows,
		"folder_items": folderItems,
		"page":         page,
		"page_size":    pageSize,
		"total_pages":  totalPages,
		"total_items":  len(rows),
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
	if isJobTrashed(job) {
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
	if isJobTrashed(job) {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	status := asString(job["status"])
	tags, err := listTagsByOwner(u.ID)
	if err != nil {
		procErrf("job.listTags", err, "owner_id=%s job_id=%s", u.ID, jobID)
	}
	selectedTags := asStringSlice(job["tags"])
	tagMap := selectedTagMap(selectedTags)
	tagText := strings.Join(selectedTags, ", ")

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
				"Tags":             tags,
				"SelectedTagsMap":  tagMap,
				"TagText":          tagText,
			})
		}
		return c.Render(http.StatusOK, "waiting.html", map[string]any{
			"Job":             toJobView(job),
			"JobID":           jobID,
			"CurrentUserName": currentUserName(c),
			"Tags":            tags,
			"SelectedTagsMap": tagMap,
			"TagText":         tagText,
		})
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
			"Tags":            tags,
			"SelectedTagsMap": tagMap,
			"TagText":         tagText,
		})
	}

	return c.Render(http.StatusOK, "waiting.html", map[string]any{
		"Job":             toJobView(job),
		"JobID":           jobID,
		"CurrentUserName": currentUserName(c),
		"Tags":            tags,
		"SelectedTagsMap": tagMap,
		"TagText":         tagText,
	})
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
	if isJobTrashed(job) {
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
	if isJobTrashed(job) {
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
		if isJobTrashed(job) {
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
	jobIDs := c.Request().PostForm["job_ids"]
	folderIDs := c.Request().PostForm["folder_ids"]
	if len(jobIDs) == 0 && len(folderIDs) == 0 {
		procLogf("[BATCH_DELETE] skipped reason=no selection")
		return c.Redirect(http.StatusSeeOther, "/jobs")
	}
	ownedJobs := make([]string, 0, len(jobIDs))
	for _, id := range jobIDs {
		job := getJob(id)
		if job == nil {
			continue
		}
		if asString(job["owner_id"]) == u.ID && !isJobTrashed(job) {
			ownedJobs = append(ownedJobs, id)
		}
	}
	for _, id := range ownedJobs {
		setJobFields(id, map[string]any{"is_trashed": true})
	}

	allFolders, _ := listAllFoldersByOwner(u.ID, true)
	selectedFolders := make(map[string]struct{}, len(folderIDs))
	for _, id := range folderIDs {
		id = normalizeFolderID(id)
		if id == "" {
			continue
		}
		f, err := getFolderByID(u.ID, id)
		if err != nil || f.IsTrashed {
			continue
		}
		selectedFolders[id] = struct{}{}
	}
	for id := range selectedFolders {
		_ = setFolderTrashed(u.ID, id, true)
	}
	subtree := make(map[string]struct{}, len(selectedFolders))
	for id := range selectedFolders {
		subtree[id] = struct{}{}
	}
	changed := true
	for changed {
		changed = false
		for _, f := range allFolders {
			if _, ok := subtree[f.ParentID]; ok {
				if _, exists := subtree[f.ID]; !exists {
					subtree[f.ID] = struct{}{}
					changed = true
				}
			}
		}
	}
	if len(subtree) > 0 {
		jobsMu.Lock()
		for _, job := range jobs {
			if asString(job["owner_id"]) != u.ID {
				continue
			}
			if _, ok := subtree[normalizeFolderID(asString(job["folder_id"]))]; ok {
				job["is_trashed"] = true
			}
		}
		saveJobsLocked()
		jobsMu.Unlock()
	}
	procLogf("[BATCH_TRASH] success jobs=%d folders=%d subtree=%d", len(ownedJobs), len(selectedFolders), len(subtree))
	return c.Redirect(http.StatusSeeOther, "/")
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
	if isJobTrashed(job) {
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

func createTagHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	name := strings.TrimSpace(c.FormValue("tag_name"))
	desc := strings.TrimSpace(c.FormValue("tag_description"))
	next := strings.TrimSpace(c.FormValue("next"))
	if next == "" {
		next = "/tags"
	}
	if !strings.HasPrefix(next, "/") {
		next = "/upload"
	}
	if !isValidTagName(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "태그명은 공백 없이 문자/숫자/_ 만 사용할 수 있습니다.")
	}
	if desc == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "태그 설명을 입력하세요.")
	}
	if err := upsertTag(u.ID, name, desc); err != nil {
		procErrf("tag.upsert", err, "owner_id=%s name=%s", u.ID, name)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 저장 실패")
	}
	procLogf("[TAG] upsert owner_id=%s name=%s", u.ID, name)
	return c.Redirect(http.StatusSeeOther, next)
}

func tagsPageHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	tags, err := listTagsByOwner(u.ID)
	if err != nil {
		procErrf("tags.list", err, "owner_id=%s", u.ID)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	return c.Render(http.StatusOK, "tags.html", map[string]any{
		"CurrentUserName": currentUserName(c),
		"Tags":            tags,
	})
}

func deleteTagHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	name := strings.TrimSpace(c.FormValue("tag_name"))
	if name == "" {
		return c.Redirect(http.StatusSeeOther, "/tags")
	}
	if err := deleteTag(u.ID, name); err != nil {
		procErrf("tag.delete", err, "owner_id=%s name=%s", u.ID, name)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 삭제 실패")
	}
	removeTagFromOwnerJobs(u.ID, name)
	procLogf("[TAG] delete owner_id=%s name=%s", u.ID, name)
	return c.Redirect(http.StatusSeeOther, "/tags")
}

func updateJobTagsHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || asString(job["owner_id"]) != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}

	selected := parseSelectedTags(c)
	allowed, err := listTagNamesByOwner(u.ID)
	if err != nil {
		procErrf("tag.listNames", err, "owner_id=%s", u.ID)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	validated := make([]string, 0, len(selected))
	for _, t := range selected {
		if _, ok := allowed[t]; ok {
			validated = append(validated, t)
		}
	}
	setJobFields(jobID, map[string]any{"tags": validated})
	procLogf("[TAG] job update job_id=%s owner_id=%s tags=%s", jobID, u.ID, strings.Join(validated, ","))
	return c.Redirect(http.StatusSeeOther, "/job/"+jobID)
}

func createFolderHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	name := strings.TrimSpace(c.FormValue("folder_name"))
	parentID := normalizeFolderID(c.FormValue("parent_id"))
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더명을 입력하세요.")
	}
	if parentID != "" {
		f, err := getFolderByID(u.ID, parentID)
		if err != nil || f.IsTrashed {
			return echo.NewHTTPError(http.StatusBadRequest, "유효하지 않은 상위 폴더입니다.")
		}
	}
	id, err := createFolder(u.ID, name, parentID)
	if err != nil {
		procErrf("folder.create", err, "owner_id=%s name=%s parent_id=%s", u.ID, name, parentID)
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 생성 실패(중복 이름 확인)")
	}
	procLogf("[FOLDER] create owner_id=%s id=%s name=%s parent_id=%s", u.ID, id, name, parentID)
	if parentID == "" {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	return c.Redirect(http.StatusSeeOther, "/?folder="+parentID)
}

func moveJobsHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	if err := c.Request().ParseForm(); err != nil {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	returnTo := safeReturnPath(c.FormValue("return_to"))
	targetFolder := normalizeFolderID(c.FormValue("target_folder_id"))
	if targetFolder != "" {
		f, err := getFolderByID(u.ID, targetFolder)
		if err != nil || f.IsTrashed {
			procErrf("batchMove.invalidTarget", err, "owner_id=%s target_folder=%s", u.ID, targetFolder)
			return c.Redirect(http.StatusSeeOther, returnTo)
		}
	}
	jobIDs := c.Request().PostForm["job_ids"]
	for _, id := range jobIDs {
		job := getJob(id)
		if job == nil || asString(job["owner_id"]) != u.ID || isJobTrashed(job) {
			continue
		}
		setJobFields(id, map[string]any{"folder_id": targetFolder})
	}
	folderIDs := c.Request().PostForm["folder_ids"]
	for _, id := range folderIDs {
		id = normalizeFolderID(id)
		if id == "" {
			continue
		}
		f, err := getFolderByID(u.ID, id)
		if err != nil || f.IsTrashed {
			continue
		}
		if targetFolder == id {
			continue
		}
		if targetFolder != "" {
			descendant, err := isFolderDescendant(u.ID, id, targetFolder)
			if err != nil || descendant {
				continue
			}
		}
		if err := moveFolder(u.ID, id, targetFolder); err != nil {
			procErrf("batchMove.folder", err, "owner_id=%s folder_id=%s target=%s", u.ID, id, targetFolder)
			continue
		}
	}
	return c.Redirect(http.StatusSeeOther, returnTo)
}

func trashHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	rows := buildJobRowsForUser(u.ID, c.QueryParam("q"), c.QueryParam("tag"), "", true)
	return c.Render(http.StatusOK, "trash.html", map[string]any{
		"JobItems":        rows,
		"CurrentUserName": currentUserName(c),
	})
}

func restoreJobHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || asString(job["owner_id"]) != u.ID {
		return c.Redirect(http.StatusSeeOther, "/trash")
	}
	folderID := normalizeFolderID(asString(job["folder_id"]))
	updates := map[string]any{"is_trashed": false}
	if folderID != "" {
		f, ferr := getFolderByID(u.ID, folderID)
		if ferr == nil {
			if f.IsTrashed {
				if err := setFolderTrashed(u.ID, folderID, false); err != nil {
					procErrf("job.restoreFolder", err, "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, folderID)
				}
			}
		} else {
			newID, err := createFolder(u.ID, "복구된 폴더", "")
			if err != nil {
				procErrf("job.restoreCreateFolder", err, "owner_id=%s job_id=%s missing_folder_id=%s", u.ID, jobID, folderID)
				updates["folder_id"] = ""
			} else {
				updates["folder_id"] = newID
				procLogf("[JOB] restore created_folder owner_id=%s job_id=%s new_folder_id=%s", u.ID, jobID, newID)
			}
		}
	}
	setJobFields(jobID, updates)
	return c.Redirect(http.StatusSeeOther, "/trash")
}

func trashJobHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || asString(job["owner_id"]) != u.ID {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	setJobFields(jobID, map[string]any{"is_trashed": true})
	return c.Redirect(http.StatusSeeOther, "/")
}

func renameJobHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || asString(job["owner_id"]) != u.ID || isJobTrashed(job) {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	nextName := strings.TrimSpace(c.FormValue("new_name"))
	if nextName == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "새 파일명을 입력하세요.")
	}
	if strings.Contains(nextName, "/") || strings.Contains(nextName, `\`) {
		return echo.NewHTTPError(http.StatusBadRequest, "파일명에 경로 문자를 사용할 수 없습니다.")
	}
	setJobFields(jobID, map[string]any{"filename": nextName})
	procLogf("[JOB] rename owner_id=%s job_id=%s new_name=%s", u.ID, jobID, nextName)
	return c.Redirect(http.StatusSeeOther, "/")
}

func restoreFolderHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	folderID := c.Param("folder_id")
	if err := setFolderTrashed(u.ID, folderID, false); err != nil {
		procErrf("folder.restore", err, "owner_id=%s folder_id=%s", u.ID, folderID)
	}
	return c.Redirect(http.StatusSeeOther, "/trash")
}

func trashFolderHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	folderID := c.Param("folder_id")
	if err := setFolderTrashed(u.ID, folderID, true); err != nil {
		procErrf("folder.trash", err, "owner_id=%s folder_id=%s", u.ID, folderID)
	}
	allFolders, _ := listAllFoldersByOwner(u.ID, true)
	subtree := map[string]struct{}{folderID: {}}
	changed := true
	for changed {
		changed = false
		for _, f := range allFolders {
			if _, ok := subtree[f.ParentID]; ok {
				if _, exists := subtree[f.ID]; !exists {
					subtree[f.ID] = struct{}{}
					changed = true
				}
			}
		}
	}

	jobsMu.Lock()
	for _, job := range jobs {
		if asString(job["owner_id"]) != u.ID {
			continue
		}
		if _, ok := subtree[normalizeFolderID(asString(job["folder_id"]))]; ok {
			job["is_trashed"] = true
		}
	}
	saveJobsLocked()
	jobsMu.Unlock()

	return c.Redirect(http.StatusSeeOther, "/")
}

func renameFolderHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	folderID := c.Param("folder_id")
	newName := strings.TrimSpace(c.FormValue("new_name"))
	if newName == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "새 폴더명을 입력하세요.")
	}
	f, err := getFolderByID(u.ID, folderID)
	if err != nil || f.IsTrashed {
		return echo.NewHTTPError(http.StatusNotFound, "폴더를 찾을 수 없습니다.")
	}
	if err := renameFolder(u.ID, folderID, newName); err != nil {
		procErrf("folder.rename", err, "owner_id=%s folder_id=%s", u.ID, folderID)
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 이름 변경 실패(중복 이름 확인)")
	}
	parent := normalizeFolderID(f.ParentID)
	if parent == "" {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	return c.Redirect(http.StatusSeeOther, "/?folder="+parent)
}

func moveFolderHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login")
	}
	folderID := c.Param("folder_id")
	targetParent := normalizeFolderID(c.FormValue("target_parent_id"))

	f, err := getFolderByID(u.ID, folderID)
	if err != nil || f.IsTrashed {
		return echo.NewHTTPError(http.StatusNotFound, "폴더를 찾을 수 없습니다.")
	}
	if targetParent == folderID {
		return echo.NewHTTPError(http.StatusBadRequest, "자기 자신으로 이동할 수 없습니다.")
	}
	if targetParent != "" {
		p, err := getFolderByID(u.ID, targetParent)
		if err != nil || p.IsTrashed {
			return echo.NewHTTPError(http.StatusBadRequest, "유효하지 않은 대상 폴더입니다.")
		}
		descendant, err := isFolderDescendant(u.ID, folderID, targetParent)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "폴더 이동 검증 실패")
		}
		if descendant {
			return echo.NewHTTPError(http.StatusBadRequest, "하위 폴더로 이동할 수 없습니다.")
		}
	}
	if err := moveFolder(u.ID, folderID, targetParent); err != nil {
		procErrf("folder.move", err, "owner_id=%s folder_id=%s target_parent=%s", u.ID, folderID, targetParent)
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 이동 실패")
	}
	if targetParent == "" {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	return c.Redirect(http.StatusSeeOther, "/?folder="+targetParent)
}
