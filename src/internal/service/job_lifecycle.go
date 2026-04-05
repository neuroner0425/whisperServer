package service

import (
	"strings"
	"time"

	"whisperserver/src/internal/store"
)

type JobLifecycleDeps struct {
	Now            func() time.Time
	CancelJob      func(string)
	RemoveTempWav  func(string)
	SetJobFields   func(string, map[string]any)
	DeleteJobBlob  func(string, string)
	ListJobBlobKinds func(string) ([]string, error)
	Notify         func(userID, eventType string, payload map[string]any)

	// Status names are still user-visible strings in the current codebase.
	StatusPending string
}

type JobLifecycle struct {
	d JobLifecycleDeps
}

func NewJobLifecycle(d JobLifecycleDeps) *JobLifecycle {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &JobLifecycle{d: d}
}

func (s *JobLifecycle) NotifyFilesChanged(userID string) {
	if s == nil || s.d.Notify == nil {
		return
	}
	s.d.Notify(userID, "files.changed", nil)
}

func (s *JobLifecycle) ResetForTranscribe(jobID string, refineEnabled bool) {
	if s == nil {
		return
	}
	if s.d.CancelJob != nil {
		s.d.CancelJob(jobID)
	}
	if s.d.RemoveTempWav != nil {
		s.d.RemoveTempWav(jobID)
	}
	if s.d.SetJobFields != nil {
		s.d.SetJobFields(jobID, map[string]any{
			"result":           "",
			"result_refined":   "",
			"refine_enabled":   refineEnabled,
			"status":           s.d.StatusPending,
			"phase":            "",
			"progress_percent": 0,
			"progress_label":   "",
			"preview_text":     "",
			"started_at":       "",
			"started_ts":       0,
			"completed_at":     "",
			"completed_ts":     0,
			"duration":         "",
			"status_detail":    "",
		})
	}
	s.deleteJobBlob(jobID, store.BlobKindPreview)
	s.deleteJobBlob(jobID, store.BlobKindTranscript)
	s.deleteJobBlob(jobID, store.BlobKindTranscriptJSON)
	s.deleteJobBlob(jobID, store.BlobKindRefined)
}

func (s *JobLifecycle) ResetForPDF(jobID string) {
	if s == nil {
		return
	}
	if s.d.CancelJob != nil {
		s.d.CancelJob(jobID)
	}
	if s.d.RemoveTempWav != nil {
		s.d.RemoveTempWav(jobID)
	}
	if s.d.SetJobFields != nil {
		s.d.SetJobFields(jobID, map[string]any{
			"result":               "",
			"result_refined":       "",
			"refine_enabled":       false,
			"status":               s.d.StatusPending,
			"phase":                "",
			"progress_percent":     0,
			"progress_label":       "",
			"preview_text":         "",
			"started_at":           "",
			"started_ts":           0,
			"completed_at":         "",
			"completed_ts":         0,
			"duration":             "",
			"status_detail":        "",
			"page_count":           0,
			"processed_page_count": 0,
			"current_chunk":        0,
			"total_chunks":         0,
			"resume_available":     false,
		})
	}
	s.ClearPDFProcessingBlobs(jobID)
}

func (s *JobLifecycle) PrepareForPDFRetry(jobID string) {
	if s == nil {
		return
	}
	if s.d.CancelJob != nil {
		s.d.CancelJob(jobID)
	}
	if s.d.RemoveTempWav != nil {
		s.d.RemoveTempWav(jobID)
	}
	if s.d.SetJobFields != nil {
		s.d.SetJobFields(jobID, map[string]any{
			"result":           "",
			"status":           s.d.StatusPending,
			"phase":            "",
			"progress_percent": 0,
			"progress_label":   "",
			"preview_text":     "",
			"completed_at":     "",
			"completed_ts":     0,
			"duration":         "",
			"status_detail":    "",
		})
	}
	s.deleteJobBlob(jobID, store.BlobKindPreview)
}

func (s *JobLifecycle) ClearPDFProcessingBlobs(jobID string) {
	s.deleteJobBlob(jobID, store.BlobKindPreview)
	s.deleteJobBlob(jobID, store.BlobKindDocumentJSON)
	s.deleteJobBlob(jobID, store.BlobKindDocumentMarkdown)
	s.deleteJobBlob(jobID, store.BlobKindDocumentChunkIndex)

	if s.d.ListJobBlobKinds == nil {
		return
	}
	kinds, err := s.d.ListJobBlobKinds(jobID)
	if err != nil {
		return
	}
	for _, kind := range kinds {
		if strings.HasPrefix(kind, "document_chunk_") {
			s.deleteJobBlob(jobID, kind)
		}
	}
}

func (s *JobLifecycle) MarkTrashed(jobID string) {
	if s == nil {
		return
	}
	if s.d.CancelJob != nil {
		s.d.CancelJob(jobID)
	}
	if s.d.RemoveTempWav != nil {
		s.d.RemoveTempWav(jobID)
	}
	if s.d.SetJobFields != nil {
		s.d.SetJobFields(jobID, map[string]any{
			"is_trashed": true,
			"deleted_at": s.d.Now().Format("2006-01-02 15:04:05"),
		})
	}
}

func (s *JobLifecycle) deleteJobBlob(jobID, kind string) {
	if s == nil || s.d.DeleteJobBlob == nil {
		return
	}
	s.d.DeleteJobBlob(jobID, kind)
}
