// upload_service.go owns upload validation, initial job creation, and post-upload handoff.
package service

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	model "whisperserver/src/internal/domain"
)

// UploadCreateRequest is the normalized request passed from transport into the upload service.
type UploadCreateRequest struct {
	OwnerID string

	// Form fields
	DisplayName     string
	Description     string
	ClientUploadID  string
	FolderID        string
	RefineRequested bool
	SelectedTags    []string
	SingleTag       string

	FileHeader *multipart.FileHeader
}

// UploadServiceDeps provides validation, storage, runtime, and side-effect hooks.
type UploadServiceDeps struct {
	// Validation
	DetectFileType func(string) string
	AllowedFile    func(string) bool
	SortedExts     func() []string

	// Tag/Folder validation + side effects
	ListTagNamesByOwner     func(string) (map[string]struct{}, error)
	GetFolderByID           func(ownerID, folderID string) (*model.Folder, error)
	TouchFolderAndAncestors func(ownerID, folderID string) error

	// IO
	SaveUploadWithLimit func(*multipart.FileHeader, string, int64, int64) (int64, error)
	IsUploadTooLarge    func(error) bool
	ConvertToAac        func(string, string) error
	GetMediaDuration    func(string) *int
	FormatSecondsPtr    func(*int) string
	SaveJobBlob         func(jobID, kind string, b []byte) error

	// Blob kinds
	BlobKindAudioAAC    string
	BlobKindPDFOriginal string

	// Runtime state mutations
	AddJob            func(string, *model.Job)
	SetJobFields      func(string, map[string]any)
	EnqueueTranscribe func(string)
	EnqueuePDFExtract func(string)

	// Side effects
	UploadBytesAdd func(float64)
	Logf           func(string, ...any)
	Errf           func(scope string, err error, format string, args ...any)

	// Config
	TmpFolder           string
	MaxUploadSizeMB     int
	UploadRateLimitKBPS int
	StatusPending       string
	StatusFailed        string

	Now      func() time.Time
	NewJobID func() string
	Spawn    func(func())
}

// UploadService validates uploads, creates jobs, and schedules follow-up work.
type UploadService struct {
	d UploadServiceDeps
}

// NewUploadService builds the upload service and applies default helpers for time/spawn.
func NewUploadService(d UploadServiceDeps) *UploadService {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Spawn == nil {
		d.Spawn = func(fn func()) { go fn() }
	}
	return &UploadService{d: d}
}

// Create validates the upload, creates the initial job, and kicks off async finalization.
func (s *UploadService) Create(req UploadCreateRequest) (jobID string, filename string, err error) {
	d := s.d

	// Basic request validation before any file IO starts.
	if strings.TrimSpace(req.OwnerID) == "" {
		return "", "", NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	if req.FileHeader == nil || strings.TrimSpace(req.FileHeader.Filename) == "" {
		return "", "", NewHTTPError(http.StatusBadRequest, "파일을 선택하세요.")
	}

	// Validate the declared file type and the optional target folder/tag set.
	originalFilename := req.FileHeader.Filename
	if d.AllowedFile == nil || !d.AllowedFile(originalFilename) {
		exts := []string{}
		if d.SortedExts != nil {
			exts = d.SortedExts()
		}
		return "", "", NewHTTPError(http.StatusBadRequest, fmt.Sprintf("허용되지 않는 파일 형식입니다. 허용: %s", strings.Join(exts, ", ")))
	}

	fileType := ""
	if d.DetectFileType != nil {
		fileType = d.DetectFileType(originalFilename)
	}
	if ct := strings.TrimSpace(req.FileHeader.Header.Get("Content-Type")); ct != "" {
		if fileType == "audio" && !strings.HasPrefix(ct, "audio/") {
			return "", "", NewHTTPError(http.StatusBadRequest, "오디오 파일 형식이 올바르지 않습니다.")
		}
		if fileType == "pdf" && ct != "application/pdf" {
			return "", "", NewHTTPError(http.StatusBadRequest, "PDF 파일 형식이 올바르지 않습니다.")
		}
	}
	if fileType != "audio" && fileType != "pdf" {
		return "", "", NewHTTPError(http.StatusBadRequest, "현재는 오디오 파일과 PDF만 업로드할 수 있습니다.")
	}

	folderID := strings.TrimSpace(req.FolderID)
	if folderID != "" && d.GetFolderByID != nil {
		f, err := d.GetFolderByID(req.OwnerID, folderID)
		if err != nil || f == nil || f.IsTrashed {
			return "", "", NewHTTPError(http.StatusBadRequest, "유효하지 않은 폴더입니다.")
		}
	}

	tags := make([]string, 0, len(req.SelectedTags)+1)
	for _, tag := range req.SelectedTags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	if t := strings.TrimSpace(req.SingleTag); t != "" {
		tags = append(tags, t)
	}
	validatedTags, err := s.validateOwnedTags(req.OwnerID, tags)
	if err != nil {
		if d.Errf != nil {
			d.Errf("upload.listTagNames", err, "owner_id=%s", req.OwnerID)
		}
		return "", "", NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}

	displayName := strings.TrimSpace(req.DisplayName)
	ext := strings.ToLower(filepath.Ext(originalFilename))
	if displayName == "" {
		displayName = strings.TrimSuffix(originalFilename, filepath.Ext(originalFilename))
	}
	if !strings.HasSuffix(strings.ToLower(displayName), ext) {
		displayName += ext
	}

	refineEnabled := req.RefineRequested
	if fileType == "pdf" {
		refineEnabled = false
	}

	if d.NewJobID == nil {
		return "", "", NewHTTPError(http.StatusInternalServerError, "업로드 처리 실패")
	}
	jobID = d.NewJobID()
	tempPath := filepath.Join(d.TmpFolder, fmt.Sprintf("%s_temp%s", jobID, ext))
	aacPath := filepath.Join(d.TmpFolder, jobID+".m4a")

	if d.SaveUploadWithLimit == nil {
		return "", "", NewHTTPError(http.StatusInternalServerError, "파일 저장 실패")
	}
	totalBytes, err := d.SaveUploadWithLimit(req.FileHeader, tempPath, int64(d.MaxUploadSizeMB)*1024*1024, int64(d.UploadRateLimitKBPS)*1024)
	if err != nil {
		_ = os.Remove(tempPath)
		if d.IsUploadTooLarge != nil && d.IsUploadTooLarge(err) {
			if d.Errf != nil {
				d.Errf("upload.save", err, "file=%s too large", originalFilename)
			}
			return "", "", NewHTTPError(http.StatusRequestEntityTooLarge, fmt.Sprintf("업로드 용량 초과(%dMB)", d.MaxUploadSizeMB))
		}
		if d.Errf != nil {
			d.Errf("upload.save", err, "file=%s", originalFilename)
		}
		return "", "", NewHTTPError(http.StatusInternalServerError, "파일 저장 실패")
	}
	if d.UploadBytesAdd != nil {
		d.UploadBytesAdd(float64(totalBytes))
	}

	// Create the initial pending job before the expensive conversion work starts.
	now := d.Now()
	job := &model.Job{
		Status:               d.StatusPending,
		Filename:             displayName,
		FileType:             fileType,
		UploadedAt:           now.Format("2006-01-02 15:04:05"),
		UploadedTS:           float64(now.Unix()),
		MediaDuration:        s.formatSecondsPtr(nil),
		MediaDurationSeconds: nil,
		Phase:                "업로드 처리 중",
		ProgressLabel:        "업로드 처리 중...",
		ClientUploadID:       strings.TrimSpace(req.ClientUploadID),
		RefineEnabled:        refineEnabled,
		OwnerID:              req.OwnerID,
		Tags:                 validatedTags,
		FolderID:             folderID,
		IsTrashed:            false,
	}
	if desc := strings.TrimSpace(req.Description); desc != "" {
		job.Description = desc
	}
	if d.AddJob != nil {
		d.AddJob(jobID, job)
	}

	if folderID != "" && d.TouchFolderAndAncestors != nil {
		if err := d.TouchFolderAndAncestors(req.OwnerID, folderID); err != nil && d.Errf != nil {
			d.Errf("upload.touchFolder", err, "owner_id=%s folder_id=%s job_id=%s", req.OwnerID, folderID, jobID)
		}
	}

	if d.Logf != nil {
		d.Logf("[UPLOAD] accepted job_id=%s filename=%s bytes=%d", jobID, displayName, totalBytes)
	}

	// Hand the heavy conversion work off to a background goroutine after the job exists.
	if fileType == "pdf" {
		d.Spawn(func() { s.finalizeUploadedPDF(jobID, tempPath) })
	} else {
		d.Spawn(func() { s.finalizeUploadedAudio(jobID, tempPath, aacPath) })
	}

	return jobID, displayName, nil
}

// validateOwnedTags drops unknown tags for the owner before the job is created.
func (s *UploadService) validateOwnedTags(ownerID string, tags []string) ([]string, error) {
	d := s.d
	if d.ListTagNamesByOwner == nil {
		return nil, fmt.Errorf("ListTagNamesByOwner is nil")
	}
	allowed, err := d.ListTagNamesByOwner(ownerID)
	if err != nil {
		return nil, err
	}
	validated := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := allowed[tag]; ok {
			validated = append(validated, tag)
		}
	}
	return validated, nil
}

// formatSecondsPtr delegates duration formatting while tolerating a missing formatter.
func (s *UploadService) formatSecondsPtr(v *int) string {
	if s.d.FormatSecondsPtr == nil {
		return ""
	}
	return s.d.FormatSecondsPtr(v)
}

func (s *UploadService) finalizeUploadedAudio(jobID, tempPath, aacPath string) {
	d := s.d
	defer func() {
		_ = os.Remove(tempPath)
		_ = os.Remove(aacPath)
	}()

	if d.ConvertToAac == nil || d.SetJobFields == nil || d.SaveJobBlob == nil {
		return
	}

	if err := d.ConvertToAac(tempPath, aacPath); err != nil {
		if d.Errf != nil {
			d.Errf("upload.convertToAac", err, "job_id=%s src=%s dst=%s", jobID, tempPath, aacPath)
		}
		d.SetJobFields(jobID, map[string]any{
			"status":         d.StatusFailed,
			"phase":          "업로드 처리 실패",
			"progress_label": "",
			"status_detail":  "ffmpeg 변환 실패",
		})
		return
	}

	aacBytes, err := os.ReadFile(aacPath)
	if err != nil {
		if d.Errf != nil {
			d.Errf("upload.readAac", err, "job_id=%s path=%s", jobID, aacPath)
		}
		d.SetJobFields(jobID, map[string]any{
			"status":         d.StatusFailed,
			"phase":          "업로드 처리 실패",
			"progress_label": "",
			"status_detail":  "업로드 파일 처리 실패",
		})
		return
	}

	if err := d.SaveJobBlob(jobID, d.BlobKindAudioAAC, aacBytes); err != nil {
		if d.Errf != nil {
			d.Errf("upload.saveAudioBlob", err, "job_id=%s", jobID)
		}
		d.SetJobFields(jobID, map[string]any{
			"status":         d.StatusFailed,
			"phase":          "업로드 처리 실패",
			"progress_label": "",
			"status_detail":  "오디오 파일 저장 실패",
		})
		return
	}

	var duration *int
	if d.GetMediaDuration != nil {
		duration = d.GetMediaDuration(aacPath)
	}
	d.SetJobFields(jobID, map[string]any{
		"media_duration_seconds": duration,
		"phase":                  "",
		"progress_label":         "",
		"status_detail":          "",
	})
	if d.EnqueueTranscribe != nil {
		d.EnqueueTranscribe(jobID)
	}
	if d.Logf != nil {
		d.Logf("[UPLOAD] queued job_id=%s", jobID)
	}
}

func (s *UploadService) finalizeUploadedPDF(jobID, tempPath string) {
	d := s.d
	defer func() { _ = os.Remove(tempPath) }()

	if d.SetJobFields == nil || d.SaveJobBlob == nil {
		return
	}

	pdfBytes, err := os.ReadFile(tempPath)
	if err != nil {
		if d.Errf != nil {
			d.Errf("upload.readPDF", err, "job_id=%s path=%s", jobID, tempPath)
		}
		d.SetJobFields(jobID, map[string]any{
			"status":         d.StatusFailed,
			"phase":          "업로드 처리 실패",
			"progress_label": "",
			"status_detail":  "PDF 파일 처리 실패",
		})
		return
	}

	if err := d.SaveJobBlob(jobID, d.BlobKindPDFOriginal, pdfBytes); err != nil {
		if d.Errf != nil {
			d.Errf("upload.savePDFBlob", err, "job_id=%s", jobID)
		}
		d.SetJobFields(jobID, map[string]any{
			"status":         d.StatusFailed,
			"phase":          "업로드 처리 실패",
			"progress_label": "",
			"status_detail":  "PDF 파일 저장 실패",
		})
		return
	}

	d.SetJobFields(jobID, map[string]any{
		"phase":          "",
		"progress_label": "",
		"status_detail":  "",
	})
	if d.EnqueuePDFExtract != nil {
		d.EnqueuePDFExtract(jobID)
	}
	if d.Logf != nil {
		d.Logf("[UPLOAD] queued pdf job_id=%s", jobID)
	}
}
