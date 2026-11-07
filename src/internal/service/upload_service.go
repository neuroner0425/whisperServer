// upload_service.go owns upload validation, initial job creation, and post-upload handoff.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
	store "whisperserver/src/internal/repo/sqlite"
)

// UploadInitiateRequest defines metadata required to issue a presigned direct upload URL.
type UploadInitiateRequest struct {
	OwnerID     string
	Filename    string
	Size        int64
	ContentType string
	FolderID    string
	Language    string
}

// UploadInitiateResponse returns the presigned upload URL and reservation parameters.
type UploadInitiateResponse struct {
	JobID      string `json:"job_id"`
	UploadURL  string `json:"upload_url"`
	StorageKey string `json:"storage_key"`
	ExpiresIn  int    `json:"expires_in"`
}

// UploadCompleteRequest notifies the backend that a client has finished direct S3/storage upload.
type UploadCompleteRequest struct {
	OwnerID     string
	JobID       string
	Filename    string
	FolderID    string
	Language    string
	Tags        []string
	Description string
}

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
	Language        string

	FileHeader *multipart.FileHeader
}

// UploadServiceDeps provides validation, storage, runtime, and side-effect hooks.
type UploadServiceDeps struct {
	TaskQueue         model.TaskQueue
	Storage           storage.Storage
	TxManager         model.TxManager
	SaveJobTx         func(model.Tx, string, *model.Job) error
	JobFinder         func(string) (*model.Job, error)
	GetJobByID        func(string) (*model.Job, error)
	InsertOutboxEvent func(context.Context, model.Tx, model.DomainEvent) (int64, error)

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
	d            UploadServiceDeps
	transcodeSem chan struct{}
}

// NewUploadService builds the upload service and applies default helpers for time/spawn.
func NewUploadService(d UploadServiceDeps) *UploadService {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Spawn == nil {
		d.Spawn = func(fn func()) { go fn() }
	}
	return &UploadService{
		d:            d,
		transcodeSem: make(chan struct{}, 4), // 최대 4개 동시 트랜스코딩 허용
	}
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
	originalFilename := filepath.Base(filepath.Clean(req.FileHeader.Filename))
	if originalFilename == "." || originalFilename == "/" || originalFilename == "" {
		return "", "", NewHTTPError(http.StatusBadRequest, "유효하지 않은 파일명입니다.")
	}
	if d.AllowedFile == nil || !d.AllowedFile(originalFilename) {
		exts := []string{}
		if d.SortedExts != nil {
			exts = d.SortedExts()
		}
		return "", "", NewHTTPError(http.StatusBadRequest, fmt.Sprintf("허용되지 않는 파일 형식입니다. 허용: %s", strings.Join(exts, ", ")))
	}

	lang := strings.ToLower(strings.TrimSpace(req.Language))
	if lang == "" {
		lang = "ko"
	} else if lang != "ko" && lang != "en" && lang != "ja" && lang != "auto" {
		return "", "", NewHTTPError(http.StatusBadRequest, "지원되지 않는 언어 코드입니다 (허용: ko, en, ja, auto).")
	}
	req.Language = lang

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

	// 매직 바이트 및 미디어 스트림 유효성 검증
	if err := validateUploadedFile(tempPath, fileType, d.GetMediaDuration); err != nil {
		_ = os.Remove(tempPath)
		if d.Errf != nil {
			d.Errf("upload.validateFile", err, "file=%s validation failed", originalFilename)
		}
		return "", "", err
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
		Phase:                "파일을 변환하는 중",
		ProgressLabel:        "파일을 변환하는 중",
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

	// If durable task queue is configured, enqueue av_scan step in SQLite.
	// Otherwise, fallback to background goroutine for backwards compatibility.
	if d.TaskQueue != nil {
		payload, _ := json.Marshal(map[string]any{
			"temp_path":      tempPath,
			"file_type":      fileType,
			"language":       req.Language,
			"refine_enabled": refineEnabled,
		})
		task := model.Task{
			ID:            fmt.Sprintf("task_%s_av_scan_%d", jobID, time.Now().UnixNano()),
			JobID:         jobID,
			OwnerID:       req.OwnerID,
			Step:          model.StepAVScan,
			ResourceClass: model.ResourceClassMedia,
			Priority:      0,
			PayloadJSON:   string(payload),
		}
		if err := d.TaskQueue.Enqueue(context.Background(), nil, task); err != nil {
			if d.Errf != nil {
				d.Errf("upload.enqueueTask", err, "job_id=%s", jobID)
			}
		}
	} else if d.Spawn != nil {
		if fileType == "pdf" {
			d.Spawn(func() { s.finalizeUploadedPDF(jobID, tempPath) })
		} else {
			d.Spawn(func() { s.finalizeUploadedAudio(jobID, tempPath, aacPath) })
		}
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
	if s.transcodeSem != nil {
		s.transcodeSem <- struct{}{}
		defer func() { <-s.transcodeSem }()
	}

	d := s.d
	defer func() {
		_ = os.Remove(tempPath)
		_ = os.Remove(aacPath)
		if r := recover(); r != nil {
			if d.Errf != nil {
				d.Errf("upload.finalizeAudio.panic", fmt.Errorf("%v", r), "job_id=%s panic recovered", jobID)
			}
			if d.SetJobFields != nil {
				d.SetJobFields(jobID, map[string]any{
					"status":         d.StatusFailed,
					"status_code":    model.JobStatusAudioConvertFailedCode,
					"phase":          "업로드 처리 실패",
					"progress_label": "",
					"status_detail":  fmt.Sprintf("변환 처리 중 예기치 못한 오류: %v", r),
				})
			}
		}
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
			"status_code":    model.JobStatusAudioConvertFailedCode,
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
			"status_code":    model.JobStatusAudioConvertFailedCode,
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
			"status_code":    model.JobStatusAudioConvertFailedCode,
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
	defer func() {
		_ = os.Remove(tempPath)
		if r := recover(); r != nil {
			if d.Errf != nil {
				d.Errf("upload.finalizePDF.panic", fmt.Errorf("%v", r), "job_id=%s panic recovered", jobID)
			}
			if d.SetJobFields != nil {
				d.SetJobFields(jobID, map[string]any{
					"status":         d.StatusFailed,
					"status_code":    model.JobStatusPDFConvertFailedCode,
					"phase":          "업로드 처리 실패",
					"progress_label": "",
					"status_detail":  fmt.Sprintf("PDF 처리 중 예기치 못한 오류: %v", r),
				})
			}
		}
	}()

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
			"status_code":    model.JobStatusPDFConvertFailedCode,
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
			"status_code":    model.JobStatusPDFConvertFailedCode,
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

func validateUploadedFile(tempPath, fileType string, getDuration func(string) *int) error {
	f, err := os.Open(tempPath)
	if err != nil {
		return NewHTTPError(http.StatusBadRequest, "업로드된 파일을 읽을 수 없습니다.")
	}
	defer f.Close()

	header := make([]byte, 512)
	n, _ := io.ReadFull(f, header)
	header = header[:n]

	detectedMIME := http.DetectContentType(header)

	switch fileType {
	case "pdf":
		// PDF: http.DetectContentType("application/pdf") 및 %PDF- 매직 바이트 교차 검증
		if detectedMIME != "application/pdf" && !bytes.HasPrefix(header, []byte("%PDF-")) {
			return NewHTTPError(http.StatusBadRequest, "파일의 실제 내용(MIME/매직 바이트)이 PDF 형식이 아닙니다.")
		}
	case "audio":
		// 1단계: Go 표준 라이브러리 net/http.DetectContentType 스니핑 검증
		isMIMEAudio := strings.HasPrefix(detectedMIME, "audio/") ||
			detectedMIME == "application/ogg" ||
			detectedMIME == "video/mp4"

		// 2단계: 표준 스니퍼가 감지하지 못하는 포맷(Raw MP3, AAC ADTS, FLAC, 순수 M4A)에 대한
		//        커스텀 매직 바이트(isAudioMagicBytes) 폴백 검증
		if !isMIMEAudio && !isAudioMagicBytes(header) {
			return NewHTTPError(http.StatusBadRequest, "파일의 실제 내용(MIME/매직 바이트)이 허용된 오디오 형식이 아닙니다.")
		}

		// 3단계: 심층 스트림 무결성 검증 (ffprobe)
		if getDuration != nil {
			duration := getDuration(tempPath)
			if duration == nil || *duration <= 0 {
				return NewHTTPError(http.StatusBadRequest, "손상되었거나 재생할 수 없는 오디오 파일입니다.")
			}
		}
	default:
		return NewHTTPError(http.StatusBadRequest, "지원되지 않는 파일 형식입니다.")
	}
	return nil
}

func isAudioMagicBytes(header []byte) bool {
	if len(header) < 4 {
		return false
	}
	// 1. WAV: RIFF....WAVE
	if len(header) >= 12 && bytes.Equal(header[0:4], []byte("RIFF")) && bytes.Equal(header[8:12], []byte("WAVE")) {
		return true
	}
	// 2. MP3: ID3 tag or sync word 0xFF 0xFB/F3/F2/E3
	if bytes.HasPrefix(header, []byte("ID3")) {
		return true
	}
	if len(header) >= 2 && header[0] == 0xFF && (header[1]&0xE0) == 0xE0 {
		return true
	}
	// 3. M4A / MP4: ....ftyp
	if len(header) >= 8 && bytes.Equal(header[4:8], []byte("ftyp")) {
		return true
	}
	// 4. AAC ADTS: 0xFF 0xF1 or 0xFF 0xF9
	if len(header) >= 2 && header[0] == 0xFF && (header[1]&0xF6) == 0xF0 {
		return true
	}
	// 5. OGG: OggS
	if bytes.HasPrefix(header, []byte("OggS")) {
		return true
	}
	// 6. FLAC: fLaC
	if bytes.HasPrefix(header, []byte("fLaC")) {
		return true
	}
	return false
}

// InitiatePresigned generates a presigned PUT URL for direct client upload into quarantine_raw.
func (s *UploadService) InitiatePresigned(ctx context.Context, req UploadInitiateRequest) (*UploadInitiateResponse, error) {
	d := s.d
	if strings.TrimSpace(req.OwnerID) == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다 (소유자 정보 누락).")
	}
	originalFilename := filepath.Base(filepath.Clean(req.Filename))
	if originalFilename == "." || originalFilename == "/" || originalFilename == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "유효하지 않은 파일명입니다.")
	}
	if d.AllowedFile == nil || !d.AllowedFile(originalFilename) {
		exts := []string{}
		if d.SortedExts != nil {
			exts = d.SortedExts()
		}
		return nil, NewHTTPError(http.StatusBadRequest, fmt.Sprintf("허용되지 않는 파일 형식입니다. 허용: %s", strings.Join(exts, ", ")))
	}

	maxUploadBytes := int64(d.MaxUploadSizeMB) * 1024 * 1024
	if maxUploadBytes > 0 && req.Size > maxUploadBytes {
		return nil, NewHTTPError(http.StatusBadRequest, fmt.Sprintf("업로드 용량 초과 (%dMB 제한)", d.MaxUploadSizeMB))
	}

	lang := strings.ToLower(strings.TrimSpace(req.Language))
	if lang == "" {
		lang = "ko"
	} else if lang != "ko" && lang != "en" && lang != "ja" && lang != "auto" {
		return nil, NewHTTPError(http.StatusBadRequest, "지원되지 않는 언어 코드입니다 (허용: ko, en, ja, auto).")
	}

	folderID := strings.TrimSpace(req.FolderID)
	if folderID != "" && d.GetFolderByID != nil {
		f, err := d.GetFolderByID(req.OwnerID, folderID)
		if err != nil || f == nil || f.IsTrashed {
			return nil, NewHTTPError(http.StatusBadRequest, "유효하지 않은 폴더입니다.")
		}
	}

	if d.Storage == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "스토리지 서비스가 구성되지 않았습니다.")
	}

	jobID := ""
	if d.NewJobID != nil {
		jobID = d.NewJobID()
	} else {
		jobID = "job-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}

	quarantineKey := fmt.Sprintf("jobs/%s/quarantine_raw", jobID)
	expires := 15 * time.Minute
	uploadURL, err := d.Storage.PresignPut(ctx, quarantineKey, req.ContentType, expires)
	if err != nil {
		return nil, fmt.Errorf("presign put failed: %w", err)
	}

	fileType := "audio"
	if d.DetectFileType != nil {
		fileType = d.DetectFileType(originalFilename)
	}

	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	nowStr := now.Format("2006-01-02 15:04:05")

	initialJob := &model.Job{
		OwnerID:     req.OwnerID,
		Filename:    originalFilename,
		FileType:    fileType,
		FolderID:    folderID,
		StatusCode:  model.JobStatusPendingCode,
		Status:      model.JobStatusName(model.JobStatusPendingCode),
		UploadedAt:  nowStr,
		UploadedTS:  float64(now.Unix()),
	}

	if d.SaveJobTx != nil {
		if err := d.SaveJobTx(nil, jobID, initialJob); err != nil {
			return nil, fmt.Errorf("save initial job failed: %w", err)
		}
	} else {
		if err := store.SaveJob(jobID, initialJob); err != nil {
			return nil, fmt.Errorf("save initial job failed: %w", err)
		}
	}

	return &UploadInitiateResponse{
		JobID:      jobID,
		UploadURL:  uploadURL,
		StorageKey: quarantineKey,
		ExpiresIn:  int(expires.Seconds()),
	}, nil
}

// CompletePresigned verifies the quarantined direct upload and enqueues the initial av_scan task atomically.
func (s *UploadService) CompletePresigned(ctx context.Context, req UploadCompleteRequest) (*model.Job, error) {
	d := s.d
	if strings.TrimSpace(req.OwnerID) == "" || strings.TrimSpace(req.JobID) == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다 (job_id 또는 owner_id 누락).")
	}

	if d.Storage == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "스토리지 서비스가 구성되지 않았습니다.")
	}

	quarantineKey := fmt.Sprintf("jobs/%s/quarantine_raw", req.JobID)
	info, err := d.Storage.Stat(ctx, quarantineKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, NewHTTPError(http.StatusBadRequest, "업로드된 미디어 객체가 존재하지 않습니다.")
		}
		return nil, fmt.Errorf("stat quarantine object failed: %w", err)
	}

	maxUploadBytes := int64(d.MaxUploadSizeMB) * 1024 * 1024
	if (maxUploadBytes > 0 && info.Size > maxUploadBytes) || info.Size <= 0 {
		_ = d.Storage.Delete(ctx, quarantineKey)
		return nil, NewHTTPError(http.StatusBadRequest, fmt.Sprintf("업로드 용량 오류 (%dMB 초과 또는 0바이트)", d.MaxUploadSizeMB))
	}

	// Ownership and existence verification (R-18 Tenant Isolation)
	var (
		existingJob *model.Job
		findErr     error
	)
	if d.JobFinder != nil {
		existingJob, findErr = d.JobFinder(req.JobID)
	} else if d.GetJobByID != nil {
		existingJob, findErr = d.GetJobByID(req.JobID)
	} else {
		existingJob, findErr = store.GetJobByID(req.JobID)
	}
	if findErr != nil {
		return nil, fmt.Errorf("find existing job failed: %w", findErr)
	}
	if existingJob == nil || existingJob.OwnerID != req.OwnerID {
		return nil, NewHTTPError(http.StatusForbidden, "해당 작업에 대한 권한이 없습니다.")
	}

	originalFilename := filepath.Base(filepath.Clean(req.Filename))
	if originalFilename == "." || originalFilename == "/" || originalFilename == "" {
		if existingJob.Filename != "" {
			originalFilename = existingJob.Filename
		} else {
			originalFilename = req.JobID
		}
	}

	fileType := existingJob.FileType
	if fileType == "" {
		fileType = "audio"
		if d.DetectFileType != nil {
			fileType = d.DetectFileType(originalFilename)
		}
	}

	lang := strings.ToLower(strings.TrimSpace(req.Language))
	if lang == "" {
		lang = "ko"
	}

	folderID := strings.TrimSpace(req.FolderID)
	if folderID == "" && existingJob.FolderID != "" {
		folderID = existingJob.FolderID
	}
	if folderID != "" && d.GetFolderByID != nil {
		f, err := d.GetFolderByID(req.OwnerID, folderID)
		if err != nil || f == nil || f.IsTrashed {
			return nil, NewHTTPError(http.StatusBadRequest, "유효하지 않은 폴더입니다.")
		}
	}

	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	nowStr := now.Format("2006-01-02 15:04:05")

	job := &model.Job{
		OwnerID:     existingJob.OwnerID,
		Filename:    originalFilename,
		FileType:    fileType,
		FolderID:    folderID,
		StatusCode:  model.JobStatusPendingCode,
		Status:      model.JobStatusName(model.JobStatusPendingCode),
		UploadedAt:  nowStr,
		UploadedTS:  float64(now.Unix()),
		Description: req.Description,
		Tags:        req.Tags,
	}

	// Atomic transaction: SaveJobTx + EnqueueTask(av_scan) + InsertOutboxEvent
	var tx model.Tx
	if d.TxManager != nil {
		tx, err = d.TxManager.BeginTx(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin transaction failed: %w", err)
		}
		defer func() {
			if tx != nil {
				_ = tx.Rollback()
			}
		}()
	}

	// 1. Save Job
	if d.SaveJobTx != nil {
		if err := d.SaveJobTx(tx, req.JobID, job); err != nil {
			return nil, fmt.Errorf("save job failed: %w", err)
		}
	}

	// 2. Enqueue Task for AV Scan
	taskPayload, _ := json.Marshal(map[string]any{
		"storage_key": quarantineKey,
		"file_type":   fileType,
		"language":    lang,
	})
	task := model.Task{
		ID:            "task-" + fmt.Sprintf("%d", time.Now().UnixNano()),
		JobID:         req.JobID,
		OwnerID:       job.OwnerID,
		Step:          model.StepAVScan,
		ResourceClass: model.ResourceClassMedia,
		Status:        "ready",
		Priority:      0,
		PayloadJSON:   string(taskPayload),
		Attempt:       0,
		MaxAttempts:   5,
	}
	if d.TaskQueue != nil {
		if err := d.TaskQueue.Enqueue(ctx, tx, task); err != nil {
			return nil, fmt.Errorf("enqueue av_scan task failed: %w", err)
		}
	}

	// 3. Outbox Event
	if d.InsertOutboxEvent != nil {
		outboxPayload, _ := json.Marshal(map[string]any{
			"job_id":      req.JobID,
			"status_code": job.StatusCode,
			"phase":       job.Status,
		})
		_, _ = d.InsertOutboxEvent(ctx, tx, model.DomainEvent{
			OwnerID:     job.OwnerID,
			JobID:       req.JobID,
			EventType:   model.EventTypeJobStatus,
			PayloadJSON: string(outboxPayload),
			CreatedAt:   now,
		})
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit upload complete failed: %w", err)
		}
		tx = nil
	}

	// Update runtime cache and notify
	if d.AddJob != nil {
		d.AddJob(req.JobID, job)
	}
	if folderID != "" && d.TouchFolderAndAncestors != nil {
		_ = d.TouchFolderAndAncestors(req.OwnerID, folderID)
	}

	return job, nil
}
