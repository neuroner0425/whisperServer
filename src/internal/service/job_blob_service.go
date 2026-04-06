package service

import "net/http"

type JobBlobServiceDeps struct {
	HasJobBlob       func(jobID, kind string) bool
	LoadJobBlob      func(jobID, kind string) ([]byte, error)
	SaveJobBlob      func(jobID, kind string, b []byte) error
	DeleteJobBlob    func(jobID, kind string)
	ListJobBlobKinds func(jobID string) ([]string, error)

	BlobKindAudioAAC           string
	BlobKindPreview            string
	BlobKindPDFOriginal        string
	BlobKindDocumentJSON       string
	BlobKindDocumentMarkdown   string
	BlobKindDocumentChunkIndex string
	BlobKindTranscript         string
	BlobKindTranscriptJSON     string
	BlobKindRefined            string
}

type JobBlobService struct {
	d JobBlobServiceDeps
}

func NewJobBlobService(d JobBlobServiceDeps) *JobBlobService {
	return &JobBlobService{d: d}
}

func (s *JobBlobService) Has(jobID, kind string) bool {
	if s == nil || s.d.HasJobBlob == nil {
		return false
	}
	return s.d.HasJobBlob(jobID, kind)
}

func (s *JobBlobService) Load(jobID, kind string) ([]byte, error) {
	if s == nil || s.d.LoadJobBlob == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	return s.d.LoadJobBlob(jobID, kind)
}

func (s *JobBlobService) Save(jobID, kind string, b []byte) error {
	if s == nil || s.d.SaveJobBlob == nil {
		return NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	return s.d.SaveJobBlob(jobID, kind, b)
}

func (s *JobBlobService) Delete(jobID, kind string) {
	if s == nil || s.d.DeleteJobBlob == nil {
		return
	}
	s.d.DeleteJobBlob(jobID, kind)
}

func (s *JobBlobService) ListKinds(jobID string) ([]string, error) {
	if s == nil || s.d.ListJobBlobKinds == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "서비스를 사용할 수 없습니다.")
	}
	return s.d.ListJobBlobKinds(jobID)
}

func (s *JobBlobService) DocumentChunkIndexKind() string {
	return s.d.BlobKindDocumentChunkIndex
}

func (s *JobBlobService) HasAudioAAC(jobID string) bool {
	return s.Has(jobID, s.d.BlobKindAudioAAC)
}

func (s *JobBlobService) LoadAudioAAC(jobID string) ([]byte, error) {
	return s.Load(jobID, s.d.BlobKindAudioAAC)
}

func (s *JobBlobService) DeletePreview(jobID string) {
	s.Delete(jobID, s.d.BlobKindPreview)
}

func (s *JobBlobService) HasPDFOriginal(jobID string) bool {
	return s.Has(jobID, s.d.BlobKindPDFOriginal)
}

func (s *JobBlobService) LoadPDFOriginal(jobID string) ([]byte, error) {
	return s.Load(jobID, s.d.BlobKindPDFOriginal)
}

func (s *JobBlobService) HasDocumentMarkdown(jobID string) bool {
	return s.Has(jobID, s.d.BlobKindDocumentMarkdown)
}

func (s *JobBlobService) LoadDocumentMarkdown(jobID string) ([]byte, error) {
	return s.Load(jobID, s.d.BlobKindDocumentMarkdown)
}

func (s *JobBlobService) LoadDocumentJSON(jobID string) ([]byte, error) {
	return s.Load(jobID, s.d.BlobKindDocumentJSON)
}

func (s *JobBlobService) SaveDocumentJSON(jobID string, b []byte) error {
	return s.Save(jobID, s.d.BlobKindDocumentJSON, b)
}

func (s *JobBlobService) SaveDocumentMarkdown(jobID string, b []byte) error {
	return s.Save(jobID, s.d.BlobKindDocumentMarkdown, b)
}

func (s *JobBlobService) LoadDocumentChunkIndex(jobID string) ([]byte, error) {
	return s.Load(jobID, s.d.BlobKindDocumentChunkIndex)
}

func (s *JobBlobService) SaveDocumentChunkIndex(jobID string, b []byte) error {
	return s.Save(jobID, s.d.BlobKindDocumentChunkIndex, b)
}

func (s *JobBlobService) HasTranscript(jobID string) bool {
	return s.Has(jobID, s.d.BlobKindTranscript)
}

func (s *JobBlobService) LoadTranscript(jobID string) ([]byte, error) {
	return s.Load(jobID, s.d.BlobKindTranscript)
}

func (s *JobBlobService) SaveTranscript(jobID string, b []byte) error {
	return s.Save(jobID, s.d.BlobKindTranscript, b)
}

func (s *JobBlobService) SaveTranscriptJSON(jobID string, b []byte) error {
	return s.Save(jobID, s.d.BlobKindTranscriptJSON, b)
}

func (s *JobBlobService) HasRefined(jobID string) bool {
	return s.Has(jobID, s.d.BlobKindRefined)
}

func (s *JobBlobService) LoadRefined(jobID string) ([]byte, error) {
	return s.Load(jobID, s.d.BlobKindRefined)
}

func (s *JobBlobService) SaveRefined(jobID string, b []byte) error {
	return s.Save(jobID, s.d.BlobKindRefined, b)
}
