package httptransport

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

// LegacyJobsHandlers serves legacy JSON/download endpoints that older templates/clients might still call:
// - GET /jobs/updates
// - GET /status/:job_id
// - GET /download/:job_id (+ refined / document-json)
// - POST /batch-download
//
// It intentionally mirrors old response shapes while using transport/service boundaries.
type LegacyJobsHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)

	FolderSvc *service.FolderService
	BlobSvc   *service.JobBlobService

	// For list/update payloads
	BuildRecentJobRows func(userID, q, tag string) []JobRow
	BuildJobRows       func(userID, q, tag, folderID string, trashed bool) []JobRow
	BuildFolderRows    func(userID, folderID, q string) []FolderRow
	RecentFolderRows   func(userID string) []FolderRow
	SortJobRows        func([]JobRow, string, string)
	SortFolderRows     func([]FolderRow, string, string)
	PaginateRows       func([]JobRow, int, int) ([]JobRow, int, int)
	SnapshotVersion    func([]JobRow, []FolderRow, int, int, int, int) string

	// For status/downloads
	GetJob          func(string) *model.Job
	StatusCompleted string

	Logf func(string, ...any)
	Errf func(string, error, string, ...any)
}

// Updates returns the legacy list payload polled by older pages.
func (h LegacyJobsHandlers) Updates() echo.HandlerFunc {
	return func(c echo.Context) error {
		disableCache(c)
		if h.CurrentUserOrUnauthorized == nil ||
			h.FolderSvc == nil ||
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

		page := ParsePositiveInt(c.QueryParam("page"), 1)
		pageSize := ParsePositiveInt(c.QueryParam("page_size"), 20)
		q := strings.TrimSpace(c.QueryParam("q"))
		tag := strings.TrimSpace(c.QueryParam("tag"))
		folderID := NormalizeFolderID(c.QueryParam("folder"))
		sortBy, sortOrder := NormalizeSortParams(c.QueryParam("sort"), c.QueryParam("order"))
		view := strings.TrimSpace(c.QueryParam("view"))
		if view == "" {
			view = "explore"
		}

		// Rebuild the same list and folder payload shape expected by the old UI.
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

		// Expand folder metadata only when the client version is stale.
		allFolders, _ := h.FolderSvc.ListAll(u.ID, false)
		path, _ := h.FolderSvc.Path(u.ID, folderID)

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
}

// Status returns lightweight progress fields for the legacy polling loop.
func (h LegacyJobsHandlers) Status() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.GetJob == nil {
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
		return c.JSON(http.StatusOK, map[string]any{
			"status":               fallback(job.Status, "알 수 없음"),
			"progress_percent":     job.ProgressPercent,
			"phase":                fallback(job.Phase, "대기 중"),
			"progress_label":       job.ProgressLabel,
			"preview_text":         job.PreviewText,
			"page_count":           job.PageCount,
			"processed_page_count": job.ProcessedPageCount,
			"current_chunk":        job.CurrentChunk,
			"total_chunks":         job.TotalChunks,
			"resume_available":     job.ResumeAvailable,
		})
	}
}

// Download returns the default downloadable result for the job.
func (h LegacyJobsHandlers) Download() echo.HandlerFunc {
	return func(c echo.Context) error {
		return h.downloadVariant(c, "default")
	}
}

// DownloadRefined returns the refined JSON result for the job.
func (h LegacyJobsHandlers) DownloadRefined() echo.HandlerFunc {
	return func(c echo.Context) error {
		return h.downloadVariant(c, "refined")
	}
}

// DownloadDocumentJSON returns the structured document JSON result for PDFs.
func (h LegacyJobsHandlers) DownloadDocumentJSON() echo.HandlerFunc {
	return func(c echo.Context) error {
		return h.downloadVariant(c, "document_json")
	}
}

// downloadVariant centralizes legacy result download behavior across variants.
func (h LegacyJobsHandlers) downloadVariant(c echo.Context, variant string) error {
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
		return echo.NewHTTPError(http.StatusNotFound, "다운로드할 결과가 없습니다.")
	}
	if strings.TrimSpace(h.StatusCompleted) != "" && job.Status != h.StatusCompleted {
		return echo.NewHTTPError(http.StatusNotFound, "다운로드할 결과가 없습니다.")
	}

	// Resolve the blob and filename suffix for the requested variant.
	var (
		b        []byte
		suffix   string
		mimeType = "text/plain; charset=utf-8"
		err      error
	)
	switch variant {
	case "refined":
		b, err = h.BlobSvc.LoadRefinedMarkdown(jobID)
		suffix = "_refined.md"
		mimeType = "text/markdown; charset=utf-8"
	case "document_json":
		b, err = h.BlobSvc.LoadDocumentJSON(jobID)
		suffix = "_document.json"
		mimeType = "application/json; charset=utf-8"
	default:
		if job.FileType == "pdf" {
			b, err = h.BlobSvc.LoadDocumentMarkdown(jobID)
			suffix = "_document.md"
			mimeType = "text/markdown; charset=utf-8"
		} else {
			b, err = h.BlobSvc.LoadTranscriptMarkdown(jobID)
			suffix = ".md"
			mimeType = "text/markdown; charset=utf-8"
		}
	}
	if err != nil {
		if h.Errf != nil {
			h.Errf("legacy.download.loadBlob", err, "job_id=%s variant=%s", jobID, variant)
		}
		// Keep legacy behavior: 404 for missing.
		msg := "다운로드할 결과가 없습니다."
		if variant == "refined" {
			msg = "정제본이 없습니다."
		}
		if variant == "document_json" {
			msg = "문서 JSON 결과가 없습니다."
		}
		return echo.NewHTTPError(http.StatusNotFound, msg)
	}

	base := strings.TrimSuffix(job.Filename, filepath.Ext(job.Filename))
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, base+suffix))
	return c.Blob(http.StatusOK, mimeType, b)
}

// BatchDownload bundles selected completed jobs into a zip archive.
func (h LegacyJobsHandlers) BatchDownload() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.GetJob == nil || h.BlobSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		if err := c.Request().ParseForm(); err != nil {
			if h.Errf != nil {
				h.Errf("legacy.batchDownload.parseForm", err, "request parse failed")
			}
			return c.Redirect(http.StatusSeeOther, filesHomePath)
		}
		ids := c.Request().PostForm["job_ids"]
		if len(ids) == 0 {
			if h.Logf != nil {
				h.Logf("[BATCH_DOWNLOAD] skipped reason=no selection")
			}
			return c.Redirect(http.StatusSeeOther, filesHomePath)
		}

		// Build the archive entirely in memory because the legacy flow is request scoped.
		buf := bytes.NewBuffer(nil)
		zw := zip.NewWriter(buf)
		added := 0
		for _, id := range ids {
			id = strings.TrimSpace(id)
			job := h.GetJob(id)
			if job == nil || job.OwnerID != u.ID || job.IsTrashed {
				continue
			}
			if strings.TrimSpace(h.StatusCompleted) != "" && job.Status != h.StatusCompleted {
				continue
			}

			var (
				b      []byte
				suffix string
				err    error
			)
			if job.FileType == "pdf" {
				suffix = "_document.md"
				b, err = h.BlobSvc.LoadDocumentMarkdown(id)
			} else if h.BlobSvc.HasRefined(id) {
				suffix = "_refined.md"
				b, err = h.BlobSvc.LoadRefinedMarkdown(id)
			} else {
				suffix = ".md"
				b, err = h.BlobSvc.LoadTranscriptMarkdown(id)
			}
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
			if h.Logf != nil {
				h.Logf("[BATCH_DOWNLOAD] skipped reason=no downloadable results selected=%d", len(ids))
			}
			return c.Redirect(http.StatusSeeOther, filesHomePath)
		}
		if h.Logf != nil {
			h.Logf("[BATCH_DOWNLOAD] success selected=%d added=%d", len(ids), added)
		}
		zipName := "whisper_results_" + time.Now().Format("20060102_150405") + ".zip"
		c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, zipName))
		return c.Blob(http.StatusOK, "application/zip", buf.Bytes())
	}
}

// fallback returns a non-empty string for legacy status payloads.
func fallback(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
