// job_blob_service.go wraps blob and JSON persistence behind a task-oriented API.
package service

import (
	"net/http"
	"sort"
)

// JobBlobServiceDeps provides blob/json repository operations and artifact kind constants.
type JobBlobServiceDeps struct {
	HasJobBlob       func(jobID, kind string) bool
	LoadJobBlob      func(jobID, kind string) ([]byte, error)
	SaveJobBlob      func(jobID, kind string, b []byte) error
	DeleteJobBlob    func(jobID, kind string)
	ListJobBlobKinds func(jobID string) ([]string, error)

	HasJobJSON       func(jobID, kind string) bool
	LoadJobJSON      func(jobID, kind string) (string, error)
	SaveJobJSON      func(jobID, kind, data string) error
	DeleteJobJSON    func(jobID, kind string)
	ListJobJSONKinds func(jobID string) ([]string, error)

	BlobKindAudioAAC           string
	BlobKindPreview            string
	BlobKindPDFOriginal        string
	BlobKindDocumentJSON       string
	BlobKindDocumentChunkIndex string
	BlobKindTranscriptJSON     string
	BlobKindRefined            string
}

// JobBlobService wraps persistence in task-oriented helper methods.
type JobBlobService struct {
	d JobBlobServiceDeps
}

// NewJobBlobService builds the service from repo callbacks and artifact kind constants.
func NewJobBlobService(d JobBlobServiceDeps) *JobBlobService {
	return &JobBlobService{d: d}
}

// HasBlob reports whether a blob kind exists for the job.
func (s *JobBlobService) HasBlob(jobID, kind string) bool {
	if s == nil || s.d.HasJobBlob == nil {
		return false
	}
	return s.d.HasJobBlob(jobID, kind)
}

// LoadBlob fetches a blob by job id and kind.
func (s *JobBlobService) LoadBlob(jobID, kind string) ([]byte, error) {
	if s == nil || s.d.LoadJobBlob == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	return s.d.LoadJobBlob(jobID, kind)
}

// SaveBlob persists a blob by job id and kind.
func (s *JobBlobService) SaveBlob(jobID, kind string, b []byte) error {
	if s == nil || s.d.SaveJobBlob == nil {
		return NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	return s.d.SaveJobBlob(jobID, kind, b)
}

// DeleteBlob removes a blob by job id and kind.
func (s *JobBlobService) DeleteBlob(jobID, kind string) {
	if s == nil || s.d.DeleteJobBlob == nil {
		return
	}
	s.d.DeleteJobBlob(jobID, kind)
}

// HasJSON reports whether a JSON kind exists for the job.
func (s *JobBlobService) HasJSON(jobID, kind string) bool {
	if s == nil || s.d.HasJobJSON == nil {
		return false
	}
	return s.d.HasJobJSON(jobID, kind)
}

// LoadJSON fetches a JSON payload by job id and kind.
func (s *JobBlobService) LoadJSON(jobID, kind string) (string, error) {
	if s == nil || s.d.LoadJobJSON == nil {
		return "", NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	return s.d.LoadJobJSON(jobID, kind)
}

// SaveJSON persists a JSON payload by job id and kind.
func (s *JobBlobService) SaveJSON(jobID, kind, data string) error {
	if s == nil || s.d.SaveJobJSON == nil {
		return NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	return s.d.SaveJobJSON(jobID, kind, data)
}

// DeleteJSON removes a JSON payload by job id and kind.
func (s *JobBlobService) DeleteJSON(jobID, kind string) {
	if s == nil || s.d.DeleteJobJSON == nil {
		return
	}
	s.d.DeleteJobJSON(jobID, kind)
}

// ListKinds returns every persisted artifact kind for the job.
func (s *JobBlobService) ListKinds(jobID string) ([]string, error) {
	if s == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	out := []string{}
	if s.d.ListJobBlobKinds != nil {
		kinds, err := s.d.ListJobBlobKinds(jobID)
		if err != nil {
			return nil, err
		}
		out = append(out, kinds...)
	}
	if s.d.ListJobJSONKinds != nil {
		kinds, err := s.d.ListJobJSONKinds(jobID)
		if err != nil {
			return nil, err
		}
		out = append(out, kinds...)
	}
	sort.Strings(out)
	return out, nil
}

// DocumentChunkIndexKind returns the blob kind used for PDF resume metadata.
func (s *JobBlobService) DocumentChunkIndexKind() string {
	return s.d.BlobKindDocumentChunkIndex
}

// HasAudioAAC reports whether the normalized audio blob exists.
func (s *JobBlobService) HasAudioAAC(jobID string) bool {
	return s.HasBlob(jobID, s.d.BlobKindAudioAAC)
}

// LoadAudioAAC loads the normalized audio blob.
func (s *JobBlobService) LoadAudioAAC(jobID string) ([]byte, error) {
	return s.LoadBlob(jobID, s.d.BlobKindAudioAAC)
}

// DeletePreview removes the persisted preview blob.
func (s *JobBlobService) DeletePreview(jobID string) {
	s.DeleteBlob(jobID, s.d.BlobKindPreview)
}

// HasPDFOriginal reports whether the original PDF blob exists.
func (s *JobBlobService) HasPDFOriginal(jobID string) bool {
	return s.HasBlob(jobID, s.d.BlobKindPDFOriginal)
}

// LoadPDFOriginal loads the original uploaded PDF blob.
func (s *JobBlobService) LoadPDFOriginal(jobID string) ([]byte, error) {
	return s.LoadBlob(jobID, s.d.BlobKindPDFOriginal)
}

// HasDocumentJSON reports whether the structured document JSON exists.
func (s *JobBlobService) HasDocumentJSON(jobID string) bool {
	return s.HasJSON(jobID, s.d.BlobKindDocumentJSON)
}

// LoadDocumentJSON loads the structured document JSON payload.
func (s *JobBlobService) LoadDocumentJSON(jobID string) ([]byte, error) {
	data, err := s.LoadJSON(jobID, s.d.BlobKindDocumentJSON)
	return []byte(data), err
}

// SaveDocumentJSON persists the structured document JSON payload.
func (s *JobBlobService) SaveDocumentJSON(jobID string, b []byte) error {
	return s.SaveJSON(jobID, s.d.BlobKindDocumentJSON, string(b))
}

// LoadDocumentMarkdown renders document JSON into markdown on demand.
func (s *JobBlobService) LoadDocumentMarkdown(jobID string) ([]byte, error) {
	data, err := s.LoadJSON(jobID, s.d.BlobKindDocumentJSON)
	if err != nil {
		return nil, err
	}
	markdown, err := RenderDocumentMarkdown(data)
	if err != nil {
		return nil, err
	}
	return []byte(markdown), nil
}

// LoadDocumentChunkIndex loads PDF resume metadata.
func (s *JobBlobService) LoadDocumentChunkIndex(jobID string) ([]byte, error) {
	return s.LoadBlob(jobID, s.d.BlobKindDocumentChunkIndex)
}

// SaveDocumentChunkIndex persists PDF resume metadata.
func (s *JobBlobService) SaveDocumentChunkIndex(jobID string, b []byte) error {
	return s.SaveBlob(jobID, s.d.BlobKindDocumentChunkIndex, b)
}

// HasTranscriptJSON reports whether structured transcript JSON exists.
func (s *JobBlobService) HasTranscriptJSON(jobID string) bool {
	return s.HasJSON(jobID, s.d.BlobKindTranscriptJSON)
}

// LoadTranscriptJSON loads the structured transcript JSON payload.
func (s *JobBlobService) LoadTranscriptJSON(jobID string) ([]byte, error) {
	data, err := s.LoadJSON(jobID, s.d.BlobKindTranscriptJSON)
	return []byte(data), err
}

// SaveTranscriptJSON persists the structured transcript JSON payload.
func (s *JobBlobService) SaveTranscriptJSON(jobID string, b []byte) error {
	return s.SaveJSON(jobID, s.d.BlobKindTranscriptJSON, string(b))
}

// LoadTranscriptTimelineText renders transcript JSON into the timeline text used by refinement.
func (s *JobBlobService) LoadTranscriptTimelineText(jobID string) (string, error) {
	data, err := s.LoadJSON(jobID, s.d.BlobKindTranscriptJSON)
	if err != nil {
		return "", err
	}
	return RenderTranscriptTimelineText(data)
}

// LoadTranscriptMarkdown renders transcript JSON into markdown on demand.
func (s *JobBlobService) LoadTranscriptMarkdown(jobID string) ([]byte, error) {
	data, err := s.LoadJSON(jobID, s.d.BlobKindTranscriptJSON)
	if err != nil {
		return nil, err
	}
	markdown, err := RenderTranscriptMarkdown(data)
	if err != nil {
		return nil, err
	}
	return []byte(markdown), nil
}

// HasRefined reports whether a refined result exists.
func (s *JobBlobService) HasRefined(jobID string) bool {
	return s.HasJSON(jobID, s.d.BlobKindRefined)
}

// LoadRefined loads the refined JSON result.
func (s *JobBlobService) LoadRefined(jobID string) ([]byte, error) {
	data, err := s.LoadJSON(jobID, s.d.BlobKindRefined)
	return []byte(data), err
}

// SaveRefined persists the refined JSON result.
func (s *JobBlobService) SaveRefined(jobID string, b []byte) error {
	return s.SaveJSON(jobID, s.d.BlobKindRefined, string(b))
}

// LoadRefinedMarkdown renders refined JSON into markdown on demand.
func (s *JobBlobService) LoadRefinedMarkdown(jobID string) ([]byte, error) {
	data, err := s.LoadJSON(jobID, s.d.BlobKindRefined)
	if err != nil {
		return nil, err
	}
	markdown, err := RenderRefinedMarkdown(data)
	if err != nil {
		return nil, err
	}
	return []byte(markdown), nil
}

// DeleteRefined removes the refined JSON payload.
func (s *JobBlobService) DeleteRefined(jobID string) {
	s.DeleteJSON(jobID, s.d.BlobKindRefined)
}
