package httpx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

func DownloadHandler(c echo.Context, deps JobsDeps) error {
	return downloadBlobHandler(c, deps, store.BlobKindTranscript, ".txt", "다운로드할 결과가 없습니다.")
}

func DownloadRefinedHandler(c echo.Context, deps JobsDeps) error {
	return downloadBlobHandler(c, deps, store.BlobKindRefined, "_refined.json", "정제본이 없습니다.")
}

func downloadBlobHandler(c echo.Context, deps JobsDeps, kind, suffix, notFoundMessage string) error {
	jobID := c.Param("job_id")
	job, _, err := deps.RequireOwnedJob(c, jobID, false)
	if err != nil {
		if _, ok := err.(*echo.HTTPError); ok {
			return echo.NewHTTPError(http.StatusNotFound, notFoundMessage)
		}
		return err
	}
	if job.Status != "완료" {
		return echo.NewHTTPError(http.StatusNotFound, notFoundMessage)
	}
	b, err := store.LoadJobBlob(jobID, kind)
	if err != nil {
		deps.Errf("download.loadBlob", err, "job_id=%s kind=%s", jobID, kind)
		return echo.NewHTTPError(http.StatusNotFound, notFoundMessage)
	}
	base := strings.TrimSuffix(job.Filename, filepath.Ext(job.Filename))
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, base+suffix))
	contentType := "text/plain; charset=utf-8"
	if kind == store.BlobKindRefined && strings.HasSuffix(suffix, ".json") {
		contentType = "application/json; charset=utf-8"
	}
	return c.Blob(http.StatusOK, contentType, b)
}

func BatchDownloadHandler(c echo.Context, deps JobsDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	if err := c.Request().ParseForm(); err != nil {
		deps.Errf("batchDownload.parseForm", err, "request parse failed")
		return c.Redirect(http.StatusSeeOther, routes.FilesHome)
	}
	ids := c.Request().PostForm["job_ids"]
	if len(ids) == 0 {
		deps.Logf("[BATCH_DOWNLOAD] skipped reason=no selection")
		return c.Redirect(http.StatusSeeOther, routes.FilesHome)
	}

	buf := bytes.NewBuffer(nil)
	zw := zip.NewWriter(buf)
	added := 0
	for _, id := range ids {
		job := deps.GetJob(id)
		if job == nil || job.Status != "완료" || job.OwnerID != u.ID || deps.IsJobTrashed(job) {
			continue
		}
		blobKind := store.BlobKindTranscript
		ext := ".txt"
		if store.HasJobBlob(id, store.BlobKindRefined) {
			blobKind = store.BlobKindRefined
			ext = "_refined.json"
		}
		b, err := store.LoadJobBlob(id, blobKind)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(job.Filename, filepath.Ext(job.Filename))
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
		deps.Logf("[BATCH_DOWNLOAD] skipped reason=no downloadable results selected=%d", len(ids))
		return c.Redirect(http.StatusSeeOther, routes.FilesHome)
	}
	deps.Logf("[BATCH_DOWNLOAD] success selected=%d added=%d", len(ids), added)
	zipName := "whisper_results_" + time.Now().Format("20060102_150405") + ".zip"
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, zipName))
	return c.Blob(http.StatusOK, "application/zip", buf.Bytes())
}
