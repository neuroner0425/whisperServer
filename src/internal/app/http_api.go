package app

import (
	"archive/zip"
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
	intutil "whisperserver/src/internal/util"
)

func apiMeJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"user": map[string]string{
			"id":          u.ID,
			"login_id":    u.LoginID,
			"email":       u.Email,
			"displayName": currentUserName(c),
		},
	})
}

func apiFilesJSONHandler(c echo.Context) error {
	disableCache(c)
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}

	q := strings.TrimSpace(c.QueryParam("q"))
	tag := strings.TrimSpace(c.QueryParam("tag"))
	view := strings.TrimSpace(c.QueryParam("view"))
	if view == "search" {
		// keep search
	} else if view != "home" {
		view = "explore"
	}
	folderID := normalizeFolderID(c.QueryParam("folder_id"))
	sortBy, sortOrder := normalizeSortParams(c.QueryParam("sort"), c.QueryParam("order"))
	page := parsePositiveInt(c.QueryParam("page"), 1)
	pageSize := parsePositiveInt(c.QueryParam("page_size"), 20)

	if view == "explore" && folderID != "" {
		f, err := store.GetFolderByID(u.ID, folderID)
		if err != nil || f.IsTrashed {
			return echo.NewHTTPError(http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		}
	}

	rows := buildRecentJobRowsForUser(u.ID, q, tag)
	folderItems := []FolderRow{}
	if view == "explore" {
		rows = buildJobRowsForUser(u.ID, q, tag, folderID, false)
		folderItems = buildFolderRowsForUser(u.ID, folderID, q)
		sortFolderRows(folderItems, sortBy, sortOrder)
	} else if view == "home" {
		folderItems = recentFolderRowsForUser(u.ID)
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

	allFolders, _ := store.ListAllFoldersByOwner(u.ID, false)
	path, _ := store.ListFolderPath(u.ID, folderID)
	tags, _ := store.ListTagsByOwner(u.ID)

	return c.JSON(http.StatusOK, map[string]any{
		"changed":           true,
		"current_user_name": currentUserName(c),
		"view_mode":         view,
		"search_query":      q,
		"selected_tag":      tag,
		"selected_sort":     sortBy,
		"selected_order":    sortOrder,
		"current_folder_id": folderID,
		"folder_path":       path,
		"all_folders":       allFolders,
		"tags":              tags,
		"job_items":         pagedRows,
		"folder_items":      folderItems,
		"page":              page,
		"page_size":         pageSize,
		"total_pages":       totalPages,
		"total_items":       len(rows),
		"version":           snapshotVersion,
		"links": map[string]string{
			"legacy_root": routes.FilesRoot,
			"legacy_home": routes.FilesHome,
		},
	})
}

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
	payload["preview_text"] = sanitizePreviewText(job.PreviewText)

	return c.JSON(http.StatusOK, payload)
}

func apiCreateFolderJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	var body struct {
		Name     string `json:"name"`
		ParentID string `json:"parent_id"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	name := strings.TrimSpace(body.Name)
	parentID := normalizeFolderID(body.ParentID)
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더명을 입력하세요.")
	}
	if parentID != "" {
		f, err := store.GetFolderByID(u.ID, parentID)
		if err != nil || f.IsTrashed {
			return echo.NewHTTPError(http.StatusBadRequest, "유효하지 않은 상위 폴더입니다.")
		}
	}
	id, err := store.CreateFolder(u.ID, name, parentID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 생성 실패(중복 이름 확인)")
	}
	if err := store.TouchFolderAndAncestors(u.ID, parentID); err != nil {
		procErrf("api.folder.createTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, id, parentID)
	}
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]any{
		"folder_id": id,
		"name":      name,
		"parent_id": parentID,
	})
}

func apiRenameFolderJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	folderID := c.Param("folder_id")
	var body struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	newName := strings.TrimSpace(body.Name)
	if newName == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "새 폴더명을 입력하세요.")
	}
	f, err := store.GetFolderByID(u.ID, folderID)
	if err != nil || f.IsTrashed {
		return echo.NewHTTPError(http.StatusNotFound, "폴더를 찾을 수 없습니다.")
	}
	if err := store.RenameFolder(u.ID, folderID, newName); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 이름 변경 실패(중복 이름 확인)")
	}
	if err := store.TouchFolderAndAncestors(u.ID, f.ParentID); err != nil {
		procErrf("api.folder.renameTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
	}
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]string{"folder_id": folderID, "name": newName})
}

func apiTrashFolderJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	folderID := c.Param("folder_id")
	f, _ := store.GetFolderByID(u.ID, folderID)
	subtree := collectFolderSubtree(u.ID, []string{folderID}, false)
	if err := store.SetFolderTrashed(u.ID, folderID, true); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 삭제 실패")
	}
	markSubtreeJobsTrashed(u.ID, subtree)
	if f != nil {
		if err := store.TouchFolderAndAncestors(u.ID, f.ParentID); err != nil {
			procErrf("api.folder.trashTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
		}
	}
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]string{"folder_id": folderID, "status": "trashed"})
}

func apiRenameJobJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID || isJobTrashed(job) {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	nextName := strings.TrimSpace(body.Name)
	if nextName == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "새 파일명을 입력하세요.")
	}
	if strings.Contains(nextName, "/") || strings.Contains(nextName, `\`) {
		return echo.NewHTTPError(http.StatusBadRequest, "파일명에 경로 문자를 사용할 수 없습니다.")
	}
	setJobFields(jobID, map[string]any{"filename": nextName})
	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "name": nextName})
}

func apiTrashJobJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	cancelJob(jobID)
	setJobFields(jobID, map[string]any{"is_trashed": true, "deleted_at": time.Now().Format("2006-01-02 15:04:05")})
	if err := store.TouchFolderAndAncestors(u.ID, job.FolderID); err != nil {
		procErrf("api.job.trashTouchFolder", err, "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, job.FolderID)
	}
	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "trashed"})
}

func apiUploadJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "파일이 없습니다.")
	}
	jobID, filename, err := createUploadedJob(c, u.ID, fileHeader)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]string{
		"job_id":   jobID,
		"filename": filename,
		"job_url":  routes.Job(jobID),
	})
}

func createUploadedJob(c echo.Context, ownerID string, fileHeader *multipart.FileHeader) (string, string, error) {
	if fileHeader.Filename == "" {
		return "", "", echo.NewHTTPError(http.StatusBadRequest, "파일을 선택하세요.")
	}
	if !uploadDeps().AllowedFile(fileHeader.Filename) {
		return "", "", echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("허용되지 않는 파일 형식입니다. 허용: %s", strings.Join(uploadDeps().SortedExts(), ", ")))
	}
	if ct := fileHeader.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "audio/") {
		return "", "", echo.NewHTTPError(http.StatusBadRequest, "현재는 오디오 파일만 업로드할 수 있습니다.")
	}
	if uploadDeps().DetectFileType(fileHeader.Filename) != "audio" {
		return "", "", echo.NewHTTPError(http.StatusBadRequest, "현재는 오디오 파일만 업로드할 수 있습니다.")
	}

	inputName := c.FormValue("display_name")
	description := strings.TrimSpace(c.FormValue("description"))
	selectedTags := parseSelectedTags(c)
	if singleTag := c.FormValue("tag"); singleTag != "" {
		selectedTags = append(selectedTags, singleTag)
	}
	folderID := normalizeFolderID(c.FormValue("folder_id"))
	allowedTags, err := store.ListTagNamesByOwner(ownerID)
	if err != nil {
		return "", "", echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}

	validatedTags := make([]string, 0, len(selectedTags))
	for _, t := range selectedTags {
		if _, ok := allowedTags[t]; ok {
			validatedTags = append(validatedTags, t)
		}
	}
	refineEnabled := uploadDeps().Truthy(c.FormValue("refine"))
	if folderID != "" {
		f, err := store.GetFolderByID(ownerID, folderID)
		if err != nil || f.IsTrashed {
			return "", "", echo.NewHTTPError(http.StatusBadRequest, "유효하지 않은 폴더입니다.")
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

	safeName := uploadDeps().SecureFilename(originalFilename)
	jobID := uuid.NewString()
	tempPath := filepath.Join(tmpFolder, fmt.Sprintf("%s_temp%s", jobID, ext))
	wavPath := filepath.Join(tmpFolder, fmt.Sprintf("%s_%s.wav", jobID, safeName))

	totalBytes, err := uploadDeps().SaveUploadWithLimit(fileHeader, tempPath, int64(maxUploadSizeMB)*1024*1024, int64(uploadRateLimitKB)*1024)
	if err != nil {
		_ = os.Remove(tempPath)
		if uploadDeps().IsUploadTooLarge(err) {
			return "", "", echo.NewHTTPError(http.StatusRequestEntityTooLarge, fmt.Sprintf("업로드 용량 초과(%dMB)", maxUploadSizeMB))
		}
		return "", "", echo.NewHTTPError(http.StatusInternalServerError, "파일 저장 실패")
	}
	uploadBytes.Add(float64(totalBytes))

	if err := uploadDeps().ConvertToWav(tempPath, wavPath); err != nil {
		_ = os.Remove(tempPath)
		return "", "", echo.NewHTTPError(http.StatusInternalServerError, "ffmpeg 변환 실패")
	}
	_ = os.Remove(tempPath)

	wavBytes, err := os.ReadFile(wavPath)
	if err != nil {
		_ = os.Remove(wavPath)
		return "", "", echo.NewHTTPError(http.StatusInternalServerError, "업로드 파일 처리 실패")
	}

	duration := uploadDeps().GetMediaDuration(wavPath)
	now := time.Now()
	job := &model.Job{
		Status:               statusPending,
		Filename:             inputName,
		FileType:             uploadDeps().DetectFileType(originalFilename),
		UploadedAt:           now.Format("2006-01-02 15:04:05"),
		UploadedTS:           float64(now.Unix()),
		MediaDuration:        uploadDeps().FormatSecondsPtr(duration),
		MediaDurationSeconds: duration,
		RefineEnabled:        refineEnabled,
		OwnerID:              ownerID,
		Tags:                 validatedTags,
		FolderID:             folderID,
		IsTrashed:            false,
	}
	if description != "" {
		job.Description = description
	}

	addJob(jobID, job)
	if err := store.TouchFolderAndAncestors(ownerID, folderID); err != nil {
		procErrf("api.upload.touchFolder", err, "owner_id=%s folder_id=%s job_id=%s", ownerID, folderID, jobID)
	}
	if err := store.SaveJobBlob(jobID, store.BlobKindWav, wavBytes); err != nil {
		_ = os.Remove(wavPath)
		deleteJobs([]string{jobID})
		return "", "", echo.NewHTTPError(http.StatusInternalServerError, "업로드 파일 저장 실패")
	}
	_ = os.Remove(wavPath)

	enqueueTranscribe(jobID)
	return jobID, inputName, nil
}

func apiTagsJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	tags, err := store.ListTagsByOwner(u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	return c.JSON(http.StatusOK, map[string]any{"tags": tags})
}

func apiCreateTagJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	name := strings.TrimSpace(body.Name)
	desc := strings.TrimSpace(body.Description)
	if !intutil.IsValidTagName(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "태그명은 공백 없이 문자/숫자/_ 만 사용할 수 있습니다.")
	}
	if desc == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "태그 설명을 입력하세요.")
	}
	if err := store.UpsertTag(u.ID, name, desc); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 저장 실패")
	}
	return c.JSON(http.StatusOK, map[string]string{"name": name, "description": desc})
}

func apiDeleteTagJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "삭제할 태그가 없습니다.")
	}
	if err := store.DeleteTag(u.ID, name); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 삭제 실패")
	}
	removeTagFromOwnerJobs(u.ID, name)
	return c.JSON(http.StatusOK, map[string]string{"name": name, "status": "deleted"})
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

func apiTrashListJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	rows := buildJobRowsForUser(u.ID, strings.TrimSpace(c.QueryParam("q")), "", "", true)
	folders, _ := store.ListAllFoldersByOwner(u.ID, true)
	return c.JSON(http.StatusOK, map[string]any{
		"job_items": rows,
		"folders":   folders,
	})
}

func apiRestoreJobJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	jobID := c.Param("job_id")
	job := getJob(jobID)
	if job == nil || job.OwnerID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	folderID := normalizeFolderID(job.FolderID)
	updates := map[string]any{"is_trashed": false, "deleted_at": ""}
	if folderID != "" {
		f, ferr := store.GetFolderByID(u.ID, folderID)
		if ferr == nil {
			if f.IsTrashed {
				_ = store.SetFolderTrashed(u.ID, folderID, false)
			}
		} else {
			newID, err := store.CreateFolder(u.ID, "복구된 폴더", "")
			if err == nil {
				updates["folder_id"] = newID
				folderID = newID
			} else {
				updates["folder_id"] = ""
				folderID = ""
			}
		}
	}
	setJobFields(jobID, updates)
	job = getJob(jobID)
	if job != nil {
		if store.HasJobBlob(jobID, store.BlobKindWav) {
			setJobFields(jobID, map[string]any{
				"status":           statusPending,
				"phase":            "",
				"progress_percent": 0,
				"progress_label":   "",
				"started_at":       "",
				"started_ts":       0,
				"completed_at":     "",
				"completed_ts":     0,
				"duration":         "",
				"status_detail":    "",
			})
			enqueueTranscribe(jobID)
		} else if store.HasJobBlob(jobID, store.BlobKindTranscript) && !store.HasJobBlob(jobID, store.BlobKindRefined) && job.RefineEnabled {
			setJobFields(jobID, map[string]any{
				"status":         statusRefiningPending,
				"progress_label": "",
				"completed_at":   "",
				"completed_ts":   0,
				"duration":       "",
				"status_detail":  "",
			})
			enqueueRefine(jobID)
		}
	}
	_ = store.TouchFolderAndAncestors(u.ID, folderID)
	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "restored"})
}

func apiRestoreFolderJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	folderID := c.Param("folder_id")
	if err := store.SetFolderTrashed(u.ID, folderID, false); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 복구 실패")
	}
	if f, err := store.GetFolderByID(u.ID, folderID); err == nil {
		_ = store.TouchFolderAndAncestors(u.ID, f.ParentID)
	}
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]string{"folder_id": folderID, "status": "restored"})
}

func apiBatchMoveJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	var body struct {
		JobIDs         []string `json:"job_ids"`
		FolderIDs      []string `json:"folder_ids"`
		TargetFolderID string   `json:"target_folder_id"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	targetFolder := normalizeFolderID(body.TargetFolderID)
	if targetFolder != "" {
		f, err := store.GetFolderByID(u.ID, targetFolder)
		if err != nil || f.IsTrashed {
			return echo.NewHTTPError(http.StatusBadRequest, "유효하지 않은 대상 폴더입니다.")
		}
	}
	touchedFolders := map[string]struct{}{}
	for _, id := range body.JobIDs {
		job := getJob(id)
		if job != nil && job.OwnerID == u.ID && !isJobTrashed(job) {
			if job.FolderID != "" {
				touchedFolders[job.FolderID] = struct{}{}
			}
			if targetFolder != "" {
				touchedFolders[targetFolder] = struct{}{}
			}
			setJobFields(id, map[string]any{"folder_id": targetFolder})
		}
	}
	for _, id := range body.FolderIDs {
		id = normalizeFolderID(id)
		if id == "" || id == targetFolder {
			continue
		}
		f, err := store.GetFolderByID(u.ID, id)
		if err != nil || f.IsTrashed {
			continue
		}
		if targetFolder != "" {
			descendant, err := store.IsFolderDescendant(u.ID, id, targetFolder)
			if err != nil || descendant {
				continue
			}
		}
		if err := store.MoveFolder(u.ID, id, targetFolder); err != nil {
			continue
		}
		if f.ParentID != "" {
			touchedFolders[f.ParentID] = struct{}{}
		}
		if targetFolder != "" {
			touchedFolders[targetFolder] = struct{}{}
		}
	}
	for id := range touchedFolders {
		_ = store.TouchFolderAndAncestors(u.ID, id)
	}
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]any{
		"target_folder_id": targetFolder,
		"job_ids":          body.JobIDs,
		"folder_ids":       body.FolderIDs,
	})
}

func clearTrashJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}

	snapshot := jobsSnapshot()
	toDelete := make([]string, 0)
	for id, job := range snapshot {
		if job.OwnerID == u.ID && job.IsTrashed {
			toDelete = append(toDelete, id)
		}
	}
	deleteJobs(toDelete)
	if err := store.DeleteTrashedFoldersByOwner(u.ID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "휴지통 비우기 실패")
	}
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]any{
		"deleted_jobs": len(toDelete),
		"status":       "cleared",
	})
}

func deleteTrashJobsJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}

	var body struct {
		JobIDs []string `json:"job_ids"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}

	snapshot := jobsSnapshot()
	toDelete := make([]string, 0, len(body.JobIDs))
	for _, id := range body.JobIDs {
		id = strings.TrimSpace(id)
		job := snapshot[id]
		if job == nil || job.OwnerID != u.ID || !job.IsTrashed {
			continue
		}
		toDelete = append(toDelete, id)
	}

	deleteJobs(toDelete)
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]any{
		"deleted_jobs": len(toDelete),
		"job_ids":      toDelete,
	})
}

func downloadFolderJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	folderID := normalizeFolderID(c.Param("folder_id"))
	if folderID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더를 찾을 수 없습니다.")
	}
	folder, err := store.GetFolderByID(u.ID, folderID)
	if err != nil || folder.IsTrashed {
		return echo.NewHTTPError(http.StatusNotFound, "폴더를 찾을 수 없습니다.")
	}

	subtree := collectFolderSubtree(u.ID, []string{folderID}, false)
	subtree[folderID] = struct{}{}
	snapshot := jobsSnapshot()
	buf := bytes.NewBuffer(nil)
	zw := zip.NewWriter(buf)
	added := 0
	for id, job := range snapshot {
		if job.OwnerID != u.ID || job.IsTrashed || job.Status != statusCompleted {
			continue
		}
		if _, ok := subtree[normalizeFolderID(job.FolderID)]; !ok {
			continue
		}
		blobKind := store.BlobKindTranscript
		suffix := ".txt"
		if store.HasJobBlob(id, store.BlobKindRefined) {
			blobKind = store.BlobKindRefined
			suffix = "_refined.txt"
		}
		b, err := store.LoadJobBlob(id, blobKind)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(job.Filename, filepath.Ext(job.Filename))
		w, err := zw.Create(base + suffix)
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
		return echo.NewHTTPError(http.StatusNotFound, "다운로드 가능한 결과가 없습니다.")
	}
	zipName := fmt.Sprintf("%s_%s.zip", folder.Name, time.Now().Format("20060102_150405"))
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, zipName))
	return c.Blob(http.StatusOK, "application/zip", buf.Bytes())
}
