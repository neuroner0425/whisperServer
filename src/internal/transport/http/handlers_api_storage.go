package httptransport

import (
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

type StorageHandlers struct {
	CapacityBytes int64

	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	JobsSnapshot              func() map[string]*model.Job
	StorageSvc                *service.StorageService
	FolderSvc                 *service.FolderService
}

type storageItem struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	FileType   string `json:"file_type"`
	FolderName string `json:"folder_name"`
	UpdatedAt  string `json:"updated_at"`
	SizeBytes  int64  `json:"size_bytes"`
}

func (h StorageHandlers) Handler() echo.HandlerFunc {
	return func(c echo.Context) error {
		disableCache(c)
		if h.CurrentUserOrUnauthorized == nil || h.JobsSnapshot == nil || h.StorageSvc == nil || h.FolderSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}

		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}

		usages, err := h.StorageSvc.UsageByOwner(u.ID)
		if err != nil {
			return toEchoHTTPError(err, http.StatusInternalServerError, "저장용량 정보를 불러오지 못했습니다.")
		}

		allFolders, _ := h.FolderSvc.ListAll(u.ID, false)
		folderMap := make(map[string]string, len(allFolders))
		for _, folder := range allFolders {
			folderMap[folder.ID] = folder.Name
		}

		snapshot := h.JobsSnapshot()
		items := make([]storageItem, 0, len(usages))
		var usedBytes int64
		for _, usage := range usages {
			job := snapshot[usage.JobID]
			if job == nil || job.OwnerID != u.ID {
				continue
			}
			folderName := "내 파일"
			if job.IsTrashed {
				folderName = "휴지통"
			} else if folderID := strings.TrimSpace(job.FolderID); folderID != "" {
				if name, ok := folderMap[folderID]; ok {
					folderName = name
				}
			}
			items = append(items, storageItem{
				ID:         usage.JobID,
				Filename:   job.Filename,
				FileType:   job.FileType,
				FolderName: folderName,
				UpdatedAt:  storageItemUpdatedAt(job),
				SizeBytes:  usage.Bytes,
			})
			usedBytes += usage.Bytes
		}

		sort.Slice(items, func(i, j int) bool {
			if items[i].SizeBytes == items[j].SizeBytes {
				return strings.ToLower(items[i].Filename) < strings.ToLower(items[j].Filename)
			}
			return items[i].SizeBytes > items[j].SizeBytes
		})

		usedRatio := 0.0
		if h.CapacityBytes > 0 {
			usedRatio = float64(usedBytes) / float64(h.CapacityBytes)
		}

		return c.JSON(http.StatusOK, map[string]any{
			"capacity_bytes":  h.CapacityBytes,
			"used_bytes":      usedBytes,
			"available_bytes": maxInt64(h.CapacityBytes-usedBytes, 0),
			"used_ratio":      usedRatio,
			"items":           items,
		})
	}
}

func storageItemUpdatedAt(job *model.Job) string {
	if job == nil {
		return ""
	}
	if strings.TrimSpace(job.CompletedAt) != "" {
		return job.CompletedAt
	}
	if strings.TrimSpace(job.StartedAt) != "" {
		return job.StartedAt
	}
	return job.UploadedAt
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func disableCache(c echo.Context) {
	c.Response().Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Expires", "0")
}
