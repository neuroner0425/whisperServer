package service

import "net/http"

type JobBlobUsage struct {
	JobID     string
	Bytes     int64
	BlobCount int
}

type StorageServiceDeps struct {
	ListJobBlobUsageByOwner func(ownerID string) ([]JobBlobUsage, error)
}

type StorageService struct {
	d StorageServiceDeps
}

func NewStorageService(d StorageServiceDeps) *StorageService {
	return &StorageService{d: d}
}

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
