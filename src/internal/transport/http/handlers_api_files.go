package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/service"
)

type FilesHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	CurrentUserName           func(echo.Context) string
	FolderSvc                 *service.FolderService
	TagSvc                    *service.TagService

	BuildRecentJobRows func(userID, q, tag string) []JobRow
	BuildJobRows       func(userID, q, tag, folderID string, trashed bool) []JobRow
	BuildFolderRows    func(userID, folderID, q string) []FolderRow
	RecentFolderRows   func(userID string) []FolderRow

	SortJobRows    func([]JobRow, string, string)
	SortFolderRows func([]FolderRow, string, string)
	PaginateRows   func([]JobRow, int, int) ([]JobRow, int, int)

	SnapshotVersion func([]JobRow, []FolderRow, int, int, int, int) string

	PDFMaxPages       int
	PDFMaxPagesPerReq int
}

func (h FilesHandlers) Handler() echo.HandlerFunc {
	return func(c echo.Context) error {
		disableCache(c)
		if h.CurrentUserOrUnauthorized == nil ||
			h.CurrentUserName == nil ||
			h.FolderSvc == nil ||
			h.TagSvc == nil ||
			h.BuildRecentJobRows == nil ||
			h.BuildJobRows == nil ||
			h.BuildFolderRows == nil ||
			h.RecentFolderRows == nil ||
			h.SortJobRows == nil ||
			h.SortFolderRows == nil ||
			h.PaginateRows == nil ||
			h.SnapshotVersion == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}

		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}

		q := strings.TrimSpace(c.QueryParam("q"))
		tag := strings.TrimSpace(c.QueryParam("tag"))
		view := strings.TrimSpace(c.QueryParam("view"))
		if view == "search" {
		} else if view != "home" {
			view = "explore"
		}
		folderID := NormalizeFolderID(c.QueryParam("folder_id"))
		sortBy, sortOrder := NormalizeSortParams(c.QueryParam("sort"), c.QueryParam("order"))
		page := ParsePositiveInt(c.QueryParam("page"), 1)
		pageSize := ParsePositiveInt(c.QueryParam("page_size"), 20)

		if view == "explore" && folderID != "" {
			if _, err := h.FolderSvc.Require(u.ID, folderID, false, http.StatusNotFound, "폴더를 찾을 수 없습니다."); err != nil {
				return toEchoHTTPError(err, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
			}
		}

		rows := h.BuildRecentJobRows(u.ID, q, tag)
		folderItems := []FolderRow{}
		if view == "explore" {
			rows = h.BuildJobRows(u.ID, q, tag, folderID, false)
			folderItems = h.BuildFolderRows(u.ID, folderID, q)
			h.SortFolderRows(folderItems, sortBy, sortOrder)
		} else if view == "home" {
			folderItems = h.RecentFolderRows(u.ID)
		}
		h.SortJobRows(rows, sortBy, sortOrder)
		pagedRows, page, totalPages := h.PaginateRows(rows, page, pageSize)
		snapshotVersion := h.SnapshotVersion(pagedRows, folderItems, page, pageSize, totalPages, len(rows))
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

		allFolders, _ := h.FolderSvc.ListAll(u.ID, false)
		path, _ := h.FolderSvc.Path(u.ID, folderID)
		tags, _ := h.TagSvc.List(u.ID)

		return c.JSON(http.StatusOK, map[string]any{
			"changed":           true,
			"current_user_name": h.CurrentUserName(c),
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
				"legacy_root": filesRootPath,
				"legacy_home": filesHomePath,
			},
			"upload_limits": map[string]any{
				"pdf_max_pages":             h.PDFMaxPages,
				"pdf_max_pages_per_request": h.PDFMaxPagesPerReq,
			},
		})
	}
}
