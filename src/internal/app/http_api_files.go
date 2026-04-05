package app

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	httpx "whisperserver/src/internal/http"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

func apiFilesJSONHandler(c echo.Context) error {
	httpx.DisableCache(c)
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil
	}

	q := strings.TrimSpace(c.QueryParam("q"))
	tag := strings.TrimSpace(c.QueryParam("tag"))
	view := strings.TrimSpace(c.QueryParam("view"))
	if view == "search" {
	} else if view != "home" {
		view = "explore"
	}
	folderID := httpx.NormalizeFolderID(c.QueryParam("folder_id"))
	sortBy, sortOrder := httpx.NormalizeSortParams(c.QueryParam("sort"), c.QueryParam("order"))
	page := httpx.ParsePositiveInt(c.QueryParam("page"), 1)
	pageSize := httpx.ParsePositiveInt(c.QueryParam("page_size"), 20)

	if view == "explore" && folderID != "" {
		if _, err := httpx.RequireFolderForOwner(u.ID, folderID, false, http.StatusNotFound, "폴더를 찾을 수 없습니다."); err != nil {
			return err
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
		"upload_limits": map[string]any{
			"pdf_max_pages":             pdfMaxPages,
			"pdf_max_pages_per_request": pdfMaxPagesPerRequest,
		},
	})
}

func apiCreateFolderJSONHandler(c echo.Context) error {
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil
	}
	var body struct {
		Name     string `json:"name"`
		ParentID string `json:"parent_id"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	name := strings.TrimSpace(body.Name)
	parentID := httpx.NormalizeFolderID(body.ParentID)
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더명을 입력하세요.")
	}
	if parentID != "" {
		if _, err := httpx.RequireFolderForOwner(u.ID, parentID, false, http.StatusBadRequest, "유효하지 않은 상위 폴더입니다."); err != nil {
			return err
		}
	}
	id, err := store.CreateFolder(u.ID, name, parentID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 생성 실패(중복 이름 확인)")
	}
	httpx.TouchFolderAncestors(u.ID, parentID, procErrf, "api.folder.createTouchParent", "owner_id=%s folder_id=%s parent_id=%s", u.ID, id, parentID)
	notifyFilesChanged(u.ID)
	return c.JSON(http.StatusOK, map[string]any{
		"folder_id": id,
		"name":      name,
		"parent_id": parentID,
	})
}

func apiRenameFolderJSONHandler(c echo.Context) error {
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil
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
	f, err := httpx.RequireFolderForOwner(u.ID, folderID, false, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
	if err != nil {
		return err
	}
	if err := store.RenameFolder(u.ID, folderID, newName); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 이름 변경 실패(중복 이름 확인)")
	}
	httpx.TouchFolderAncestors(u.ID, f.ParentID, procErrf, "api.folder.renameTouchParent", "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
	notifyFilesChanged(u.ID)
	return c.JSON(http.StatusOK, map[string]string{"folder_id": folderID, "name": newName})
}

func apiTrashFolderJSONHandler(c echo.Context) error {
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil
	}
	folderID := c.Param("folder_id")
	f, _ := httpx.RequireFolderForOwner(u.ID, folderID, true, http.StatusBadRequest, "폴더 삭제 실패")
	subtree := collectFolderSubtree(u.ID, []string{folderID}, false)
	if err := store.SetFolderTrashed(u.ID, folderID, true); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 삭제 실패")
	}
	markSubtreeJobsTrashed(u.ID, subtree)
	if f != nil {
		httpx.TouchFolderAncestors(u.ID, f.ParentID, procErrf, "api.folder.trashTouchParent", "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
	}
	notifyFilesChanged(u.ID)
	return c.JSON(http.StatusOK, map[string]string{"folder_id": folderID, "status": "trashed"})
}

func apiRenameJobJSONHandler(c echo.Context) error {
	_, _, err := requireOwnedJobOrNotFound(c, false)
	if err != nil {
		return err
	}
	jobID := c.Param("job_id")
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
	u, job, err := requireOwnedJobOrNotFound(c, true)
	if err != nil {
		return err
	}
	jobID := c.Param("job_id")
	markJobTrashed(jobID)
	httpx.TouchFolderAncestors(u.ID, job.FolderID, procErrf, "api.job.trashTouchFolder", "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, job.FolderID)
	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID, "status": "trashed"})
}
