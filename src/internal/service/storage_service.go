// storage_service.go exposes storage usage queries used by the storage page API.
package service

import "net/http"

// JobBlobUsage is the service-layer storage usage row for one job.
type JobBlobUsage struct {
	JobID     string
	Bytes     int64
	BlobCount int
}

// StorageJobItem is the service-layer storage row with both usage and job metadata.
type StorageJobItem struct {
	JobID       string
	Filename    string
	FileType    string
	FolderID    string
	IsTrashed   bool
	UploadedTS  float64
	StartedTS   float64
	CompletedTS float64
	SizeBytes   int64
	BlobCount   int
}

// StorageServiceDeps provides repository callbacks used by storage queries.
type StorageServiceDeps struct {
	ListJobBlobUsageByOwner     func(ownerID string) ([]JobBlobUsage, error)
	ListStorageJobItemsByOwner func(ownerID string) ([]StorageJobItem, error)
}

// StorageService exposes storage usage queries to HTTP handlers.
type StorageService struct {
	d StorageServiceDeps
}

// NewStorageService builds the storage service from repo callbacks.
func NewStorageService(d StorageServiceDeps) *StorageService {
	return &StorageService{d: d}
}

// UsageByOwner returns aggregated blob usage for every job owned by the user.
func (s *StorageService) UsageByOwner(ownerID string) ([]JobBlobUsage, error) {
	if s == nil || s.d.ListJobBlobUsageByOwner == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	usages, err := s.d.ListJobBlobUsageByOwner(ownerID)
	if err != nil {
		return nil, NewHTTPError(http.StatusInternalServerError, "저장용량 정보를 불러오지 못했습니다.")
	}
	return usages, nil
}

// ListStorageItemsByOwner returns aggregated blob usage and job metadata for every job owned by the user.
func (s *StorageService) ListStorageItemsByOwner(ownerID string) ([]StorageJobItem, error) {
	if s == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	if s.d.ListStorageJobItemsByOwner != nil {
		items, err := s.d.ListStorageJobItemsByOwner(ownerID)
		if err != nil {
			return nil, NewHTTPError(http.StatusInternalServerError, "저장용량 정보를 불러오지 못했습니다.")
		}
		return items, nil
	}
	return nil, nil
}
