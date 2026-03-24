package app

import (
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/store"
)

const storageCapacityBytes int64 = 5 * 1024 * 1024 * 1024

type storageItem struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	FileType   string `json:"file_type"`
	FolderName string `json:"folder_name"`
	UpdatedAt  string `json:"updated_at"`
	SizeBytes  int64  `json:"size_bytes"`
}

func apiStorageJSONHandler(c echo.Context) error {
	disableCache(c)
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}

	usages, err := store.ListJobBlobUsageByOwner(u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "저장용량 정보를 불러오지 못했습니다.")
	}

	allFolders, _ := store.ListAllFoldersByOwner(u.ID, false)
	folderMap := make(map[string]string, len(allFolders))
	for _, folder := range allFolders {
		folderMap[folder.ID] = folder.Name
	}

	snapshot := jobsSnapshot()
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
	if storageCapacityBytes > 0 {
		usedRatio = float64(usedBytes) / float64(storageCapacityBytes)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"capacity_bytes":  storageCapacityBytes,
		"used_bytes":      usedBytes,
		"available_bytes": maxInt64(storageCapacityBytes-usedBytes, 0),
		"used_ratio":      usedRatio,
		"items":           items,
	})
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
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
