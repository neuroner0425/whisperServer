package httpx

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

func JobsHandler(c echo.Context, deps JobsDeps) error {
	deps.DisableCache(c)
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	q := c.QueryParam("q")
	tag := c.QueryParam("tag")
	sortBy, sortOrder := deps.NormalizeSortParams(c.QueryParam("sort"), c.QueryParam("order"))

	view := "home"
	folderID := ""
	switch pathStr := c.Path(); {
	case strings.HasPrefix(pathStr, routes.FilesRoot):
		view = "explore"
	case strings.HasPrefix(pathStr, routes.Files+"/folders/"):
		view = "explore"
		folderID = c.Param("folder_id")
	case pathStr == routes.FilesHome:
		view = "home"
	}

	folderID = deps.NormalizeFolderID(folderID)
	page := deps.ParsePositiveInt(c.QueryParam("page"), 1)
	pageSize := deps.ParsePositiveInt(c.QueryParam("page_size"), 20)
	if view == "explore" && folderID != "" {
		f, err := store.GetFolderByID(u.ID, folderID)
		if err != nil || f.IsTrashed {
			return echo.NewHTTPError(http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		}
	}

	rows := deps.BuildRecentJobRows(u.ID, q, tag)
	folderItems := []FolderRow{}
	if view == "explore" {
		rows = deps.BuildJobRows(u.ID, q, tag, folderID, false)
		folderItems = deps.BuildFolderRows(u.ID, folderID, q)
		deps.SortFolderRows(folderItems, sortBy, sortOrder)
	} else if view == "home" {
		folderItems = deps.RecentFolderRows(u.ID)
	}
	deps.SortJobRows(rows, sortBy, sortOrder)
	pagedRows, page, totalPages := deps.PaginateRows(rows, page, pageSize)
	snapshotVersion := deps.JobsSnapshotVersion(pagedRows, folderItems, page, pageSize, totalPages, len(rows))

	tags, err := store.ListTagsByOwner(u.ID)
	if err != nil {
		deps.Errf("jobs.listTags", err, "owner_id=%s", u.ID)
	}
	folders, err := store.ListFoldersByParent(u.ID, folderID, false)
	if err != nil {
		deps.Errf("jobs.listFoldersByParent", err, "owner_id=%s folder=%s", u.ID, folderID)
	}
	allFolders, err := store.ListAllFoldersByOwner(u.ID, false)
	if err != nil {
		deps.Errf("jobs.listAllFolders", err, "owner_id=%s", u.ID)
	}
	allFoldersJSON, _ := json.Marshal(allFolders)
	path, err := store.ListFolderPath(u.ID, folderID)
	if err != nil {
		deps.Errf("jobs.listFolderPath", err, "owner_id=%s folder=%s", u.ID, folderID)
	}

	return c.Render(http.StatusOK, "files_index.html", map[string]any{
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
		"CurrentUserName": deps.CurrentUserName(c),
		"Page":            page,
		"PageSize":        pageSize,
		"TotalPages":      totalPages,
		"SnapshotVersion": snapshotVersion,
		"SelectedSort":    sortBy,
		"SelectedOrder":   sortOrder,
	})
}

func JobsUpdatesHandler(c echo.Context, deps JobsDeps) error {
	deps.DisableCache(c)
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	page := deps.ParsePositiveInt(c.QueryParam("page"), 1)
	pageSize := deps.ParsePositiveInt(c.QueryParam("page_size"), 20)
	q := c.QueryParam("q")
	tag := c.QueryParam("tag")
	folderID := c.QueryParam("folder")
	sortBy, sortOrder := deps.NormalizeSortParams(c.QueryParam("sort"), c.QueryParam("order"))
	view := strings.TrimSpace(c.QueryParam("view"))
	if view == "" {
		view = "explore"
	}

	rows := deps.BuildRecentJobRows(u.ID, q, tag)
	folderItems := []FolderRow{}
	if view == "explore" {
		rows = deps.BuildJobRows(u.ID, q, tag, folderID, false)
		folderItems = deps.BuildFolderRows(u.ID, folderID, q)
		deps.SortFolderRows(folderItems, sortBy, sortOrder)
	} else if view == "home" {
		folderItems = deps.RecentFolderRows(u.ID)
	}
	deps.SortJobRows(rows, sortBy, sortOrder)
	pagedRows, page, totalPages := deps.PaginateRows(rows, page, pageSize)
	snapshotVersion := deps.JobsSnapshotVersion(pagedRows, folderItems, page, pageSize, totalPages, len(rows))
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
	return c.JSON(http.StatusOK, map[string]any{
		"changed":      true,
		"version":      snapshotVersion,
		"job_items":    pagedRows,
		"folder_items": folderItems,
		"all_folders":  allFolders,
		"folder_path":  path,
		"page":         page,
		"total_pages":  totalPages,
	})
}

func TrashPageHandler(c echo.Context, deps JobsDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	rows := deps.BuildJobRows(u.ID, c.QueryParam("q"), c.QueryParam("tag"), "", true)
	return c.Render(http.StatusOK, "files_trash.html", map[string]any{
		"JobItems":        rows,
		"CurrentUserName": deps.CurrentUserName(c),
	})
}

func StatusHandler(c echo.Context, deps JobsDeps) error {
	job, _, err := deps.RequireOwnedJob(c, c.Param("job_id"), false)
	if err != nil {
		if he, ok := err.(*echo.HTTPError); ok && he.Code == http.StatusNotFound {
			return err
		}
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"status":               deps.Fallback(job.Status, "알 수 없음"),
		"progress_percent":     job.ProgressPercent,
		"phase":                deps.Fallback(job.Phase, "대기 중"),
		"progress_label":       job.ProgressLabel,
		"preview_text":         job.PreviewText,
		"page_count":           job.PageCount,
		"processed_page_count": job.ProcessedPageCount,
		"current_chunk":        job.CurrentChunk,
		"total_chunks":         job.TotalChunks,
		"resume_available":     job.ResumeAvailable,
	})
}

func JobHandler(c echo.Context, deps JobsDeps) error {
	jobID := c.Param("job_id")
	job, u, err := deps.RequireOwnedJob(c, jobID, false)
	if err != nil {
		return err
	}

	status := job.Status
	tags, err := store.ListTagsByOwner(u.ID)
	if err != nil {
		deps.Errf("job.listTags", err, "owner_id=%s job_id=%s", u.ID, jobID)
	}
	selectedTags := job.Tags
	tagMap := deps.SelectedTagMap(selectedTags)
	tagText := strings.Join(selectedTags, ", ")

	if status == "정제 대기 중" || status == "정제 중" {
		if store.HasJobBlob(jobID, store.BlobKindTranscript) {
			b, err := store.LoadJobBlob(jobID, store.BlobKindTranscript)
			if err != nil {
				deps.Errf("job.loadTranscriptBlob", err, "job_id=%s", jobID)
				return echo.NewHTTPError(http.StatusInternalServerError, "원본 결과 읽기 실패")
			}
			esc := html.EscapeString(string(b))
			return c.Render(http.StatusOK, "job_preview.html", map[string]any{
				"Job":              deps.ToJobView(job),
				"JobID":            jobID,
				"OriginalTextHTML": strings.ReplaceAll(esc, "\n", "<br>"),
				"CurrentUserName":  deps.CurrentUserName(c),
				"Tags":             tags,
				"SelectedTagsMap":  tagMap,
				"TagText":          tagText,
			})
		}
		return renderWaitingPage(c, deps, job, jobID, tags, tagMap, tagText)
	}

	if status == "완료" {
		if job.FileType == "pdf" {
			if !store.HasJobBlob(jobID, store.BlobKindDocumentMarkdown) {
				return echo.NewHTTPError(http.StatusNotFound, "결과 파일을 찾을 수 없습니다.")
			}
			b, err := store.LoadJobBlob(jobID, store.BlobKindDocumentMarkdown)
			if err != nil {
				deps.Errf("job.loadDocumentMarkdownBlob", err, "job_id=%s", jobID)
				return echo.NewHTTPError(http.StatusInternalServerError, "결과 읽기 실패")
			}
			return c.Render(http.StatusOK, "job_result.html", map[string]any{
				"Job":             deps.ToJobView(job),
				"JobID":           jobID,
				"Text":            deps.RenderMarkdownText(string(b)),
				"Variant":         "document",
				"HasRefined":      false,
				"CanRefine":       false,
				"CurrentUserName": deps.CurrentUserName(c),
				"Tags":            tags,
				"SelectedTagsMap": tagMap,
				"TagText":         tagText,
				"IsPDF":           true,
				"HasDocumentJSON": store.HasJobBlob(jobID, store.BlobKindDocumentJSON),
			})
		}
		showOriginal := strings.TrimSpace(c.QueryParam("original")) == "1" || strings.TrimSpace(c.QueryParam("original")) == "true"
		hasRefined := store.HasJobBlob(jobID, store.BlobKindRefined)
		useRefined := hasRefined && !showOriginal
		blobKind := store.BlobKindTranscript
		if useRefined {
			blobKind = store.BlobKindRefined
		}
		if !store.HasJobBlob(jobID, blobKind) {
			return echo.NewHTTPError(http.StatusNotFound, "결과 파일을 찾을 수 없습니다.")
		}
		b, err := store.LoadJobBlob(jobID, blobKind)
		if err != nil {
			deps.Errf("job.loadResultBlob", err, "job_id=%s kind=%s", jobID, blobKind)
			return echo.NewHTTPError(http.StatusInternalServerError, "결과 읽기 실패")
		}
		return c.Render(http.StatusOK, "job_result.html", map[string]any{
			"Job":             deps.ToJobView(job),
			"JobID":           jobID,
			"Text":            deps.RenderResultText(string(b), !useRefined, job.MediaDurationSeconds),
			"Variant":         map[bool]string{true: "original", false: "refined"}[!useRefined],
			"HasRefined":      hasRefined,
			"CanRefine":       deps.HasGeminiConfigured(),
			"CurrentUserName": deps.CurrentUserName(c),
			"Tags":            tags,
			"SelectedTagsMap": tagMap,
			"TagText":         tagText,
		})
	}

	return renderWaitingPage(c, deps, job, jobID, tags, tagMap, tagText)
}

func renderWaitingPage(c echo.Context, deps JobsDeps, job *model.Job, jobID string, tags []model.Tag, tagMap map[string]bool, tagText string) error {
	return c.Render(http.StatusOK, "job_waiting.html", map[string]any{
		"Job":             deps.ToJobView(job),
		"JobID":           jobID,
		"CurrentUserName": deps.CurrentUserName(c),
		"Tags":            tags,
		"SelectedTagsMap": tagMap,
		"TagText":         tagText,
	})
}
