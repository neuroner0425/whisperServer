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

	"whisperserver/src/internal/model"
	"whisperserver/src/internal/service"
)

type FolderDownloadHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	FolderSvc                 *service.FolderService
	BlobSvc                   *service.JobBlobService
	JobsSnapshot              func() map[string]*model.Job
	CollectFolderSubtree      func(userID string, folderIDs []string, trashFolders bool) map[string]struct{}

	StatusCompleted string
}

func (h FolderDownloadHandlers) Handler() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.FolderSvc == nil || h.BlobSvc == nil || h.JobsSnapshot == nil || h.CollectFolderSubtree == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		folderID := NormalizeFolderID(c.Param("folder_id"))
		if folderID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "폴더를 찾을 수 없습니다.")
		}
		folder, err := h.FolderSvc.Require(u.ID, folderID, false, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		if err != nil {
			return toEchoHTTPError(err, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		}

		subtree := h.CollectFolderSubtree(u.ID, []string{folderID}, false)
		subtree[folderID] = struct{}{}
		snapshot := h.JobsSnapshot()
		buf := bytes.NewBuffer(nil)
		zw := zip.NewWriter(buf)
		added := 0
		for id, job := range snapshot {
			if job == nil || job.OwnerID != u.ID || job.IsTrashed || job.Status != h.StatusCompleted {
				continue
			}
			if _, ok := subtree[NormalizeFolderID(job.FolderID)]; !ok {
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
				suffix = "_refined.json"
				b, err = h.BlobSvc.LoadRefined(id)
			} else {
				suffix = ".txt"
				b, err = h.BlobSvc.LoadTranscript(id)
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
			return echo.NewHTTPError(http.StatusNotFound, "다운로드 가능한 결과가 없습니다.")
		}
		zipName := fmt.Sprintf("%s_%s.zip", folder.Name, time.Now().Format("20060102_150405"))
		c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, zipName))
		return c.Blob(http.StatusOK, "application/zip", buf.Bytes())
	}
}
