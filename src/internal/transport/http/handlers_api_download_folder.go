package httptransport

import (
	"archive/zip"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

type CompletedJobRecord struct {
	ID  string
	Job *model.Job
}

// FolderDownloadHandlers bundles all completed results from a folder subtree into a zip.
type FolderDownloadHandlers struct {
	CurrentUserOrUnauthorized    func(echo.Context) (*User, bool)
	FolderSvc                    *service.FolderService
	BlobSvc                      *service.JobBlobService
	JobsSnapshot                 func() map[string]*model.Job
	ListCompletedJobsByFolderIDs func(ownerID string, folderIDs []string) ([]CompletedJobRecord, error)
	CollectFolderSubtree         func(userID string, folderIDs []string, trashFolders bool) map[string]struct{}

	StatusCompleted string
	Errf            func(scope string, err error, format string, args ...any)
}

// Handler creates and streams the folder result archive on demand.
func (h FolderDownloadHandlers) Handler() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.FolderSvc == nil || h.BlobSvc == nil || (h.JobsSnapshot == nil && h.ListCompletedJobsByFolderIDs == nil) || h.CollectFolderSubtree == nil {
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

		var targetJobs []CompletedJobRecord
		if h.ListCompletedJobsByFolderIDs != nil {
			fIDs := make([]string, 0, len(subtree))
			for fid := range subtree {
				fIDs = append(fIDs, fid)
			}
			records, err := h.ListCompletedJobsByFolderIDs(u.ID, fIDs)
			if err == nil {
				targetJobs = records
			}
		} else if h.JobsSnapshot != nil {
			snapshot := h.JobsSnapshot()
			for id, job := range snapshot {
				if job == nil || job.OwnerID != u.ID || job.IsTrashed || job.Status != h.StatusCompleted {
					continue
				}
				if _, ok := subtree[NormalizeFolderID(job.FolderID)]; ok {
					targetJobs = append(targetJobs, CompletedJobRecord{ID: id, Job: job})
				}
			}
		}

		if len(targetJobs) == 0 {
			return echo.NewHTTPError(http.StatusNotFound, "다운로드 가능한 결과가 없습니다.")
		}
		if len(targetJobs) > MaxBatchDownloadJobs {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("한 번에 최대 %d개의 작업만 다운로드할 수 있습니다.", MaxBatchDownloadJobs))
		}

		res := c.Response()
		zipName := fmt.Sprintf("%s_%s.zip", folder.Name, time.Now().Format("20060102_150405"))
		res.Header().Set("Content-Type", "application/zip")
		setContentDisposition(c, zipName)
		res.WriteHeader(http.StatusOK)

		zw := zip.NewWriter(res.Writer)
		added := 0
		var cumulativeBytes int64
		seenNames := make(map[string]int)
		for _, rec := range targetJobs {
			if c.Request().Context().Err() != nil {
				if h.Errf != nil {
					h.Errf("downloadFolder.canceled", c.Request().Context().Err(), "client request context canceled")
				}
				break
			}

			id := rec.ID
			job := rec.Job
			if job == nil {
				continue
			}
			var (
				b      []byte
				suffix string
				err    error
			)
			if job.FileType == "pdf" {
				suffix = ".md"
				b, err = h.BlobSvc.LoadDocumentMarkdown(id)
			} else if h.BlobSvc.HasRefined(id) {
				suffix = ".md"
				b, err = h.BlobSvc.LoadRefinedMarkdown(id)
			} else {
				suffix = ".md"
				b, err = h.BlobSvc.LoadTranscriptMarkdown(id)
			}

			base := strings.TrimSuffix(job.Filename, filepath.Ext(job.Filename))
			if base == "" {
				base = id
			}

			if err != nil {
				errEntry := fmt.Sprintf("_ERROR_%s_%s.txt", base, id)
				if w, createErr := zw.Create(errEntry); createErr == nil {
					_, _ = w.Write([]byte(fmt.Sprintf("Failed to load artifact for job %s: %v\n", id, err)))
				}
				continue
			}

			b = []byte(service.RenderDownloadMarkdownTitle(base, string(b)))

			if cumulativeBytes+int64(len(b)) > MaxBatchDownloadBytes {
				if w, createErr := zw.Create("_WARNING_TRUNCATED.txt"); createErr == nil {
					_, _ = w.Write([]byte(fmt.Sprintf("Cumulative uncompressed size exceeded limit of %d MB. Remaining items truncated.\n", MaxBatchDownloadBytes/(1024*1024))))
				}
				break
			}
			cumulativeBytes += int64(len(b))

			entryName := base + suffix
			if count, exists := seenNames[entryName]; exists {
				seenNames[entryName] = count + 1
				entryName = fmt.Sprintf("%s (%d)%s", base, count, suffix)
			} else {
				seenNames[entryName] = 1
			}

			w, err := zw.Create(entryName)
			if err != nil {
				continue
			}
			if _, err := w.Write(b); err != nil {
				if h.Errf != nil {
					h.Errf("downloadFolder.clientDisconnected", err, "client disconnected prematurely")
				}
				break
			}
			added++
		}
		_ = zw.Close()
		return nil
	}
}
