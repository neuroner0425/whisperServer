package httpx

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

type UploadDeps struct {
	CurrentUser         func(echo.Context) (*User, error)
	CurrentUserName     func(echo.Context) string
	ParseSelectedTags   func(echo.Context) []string
	NormalizeFolderID   func(string) string
	Truthy              func(string) bool
	AllowedFile         func(string) bool
	SortedExts          func() []string
	SecureFilename      func(string) string
	SaveUploadWithLimit func(*multipart.FileHeader, string, int64) (int64, error)
	IsUploadTooLarge    func(error) bool
	ConvertToWav        func(string, string) error
	GetMediaDuration    func(string) *int
	FormatSecondsPtr    func(*int) string
	AddJob              func(string, *model.Job)
	DeleteJobs          func([]string)
	EnqueueTranscribe   func(string)
	Logf                func(string, ...any)
	Errf                func(string, error, string, ...any)
	UploadBytesAdd      func(float64)
	TmpFolder           string
	MaxUploadSizeMB     int
	StatusPending       string
}

func UploadGetHandler(c echo.Context, deps UploadDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	tags, err := store.ListTagsByOwner(u.ID)
	if err != nil {
		deps.Errf("upload.listTags", err, "owner_id=%s", u.ID)
	}
	folders, err := store.ListAllFoldersByOwner(u.ID, false)
	if err != nil {
		deps.Errf("upload.listFolders", err, "owner_id=%s", u.ID)
	}
	return c.Render(http.StatusOK, "files_upload.html", map[string]any{
		"CurrentUserName": deps.CurrentUserName(c),
		"Tags":            tags,
		"Folders":         folders,
	})
}

func UploadPostHandler(c echo.Context, deps UploadDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	deps.Logf("[UPLOAD] request received")
	fileHeader, err := c.FormFile("file")
	if err != nil {
		deps.Errf("upload.formFile", err, "missing file")
		return echo.NewHTTPError(http.StatusBadRequest, "파일이 없습니다.")
	}
	if fileHeader.Filename == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "파일을 선택하세요.")
	}
	if !deps.AllowedFile(fileHeader.Filename) {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("허용되지 않는 파일 형식입니다. 허용: %s", strings.Join(deps.SortedExts(), ", ")))
	}
	if ct := fileHeader.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "audio/") && !strings.HasPrefix(ct, "video/") {
		return echo.NewHTTPError(http.StatusBadRequest, "오디오/비디오 파일만 업로드할 수 있습니다.")
	}

	inputName := c.FormValue("display_name")
	description := strings.TrimSpace(c.FormValue("description"))
	selectedTags := deps.ParseSelectedTags(c)
	if singleTag := c.FormValue("tag"); singleTag != "" {
		selectedTags = append(selectedTags, singleTag)
	}
	folderID := deps.NormalizeFolderID(c.FormValue("folder_id"))
	allowedTags, err := store.ListTagNamesByOwner(u.ID)
	if err != nil {
		deps.Errf("upload.listTagNames", err, "owner_id=%s", u.ID)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}

	validatedTags := make([]string, 0, len(selectedTags))
	for _, t := range selectedTags {
		if _, ok := allowedTags[t]; ok {
			validatedTags = append(validatedTags, t)
		}
	}
	refineEnabled := deps.Truthy(c.FormValue("refine"))
	if folderID != "" {
		f, err := store.GetFolderByID(u.ID, folderID)
		if err != nil || f.IsTrashed {
			return echo.NewHTTPError(http.StatusBadRequest, "유효하지 않은 폴더입니다.")
		}
	}

	originalFilename := fileHeader.Filename
	ext := strings.ToLower(filepath.Ext(originalFilename))
	if inputName == "" {
		inputName = strings.TrimSuffix(originalFilename, filepath.Ext(originalFilename))
	}
	if !strings.HasSuffix(strings.ToLower(inputName), ext) {
		inputName += ext
	}

	safeName := deps.SecureFilename(originalFilename)
	jobID := uuid.NewString()
	tempPath := filepath.Join(deps.TmpFolder, fmt.Sprintf("%s_temp%s", jobID, ext))
	wavPath := filepath.Join(deps.TmpFolder, fmt.Sprintf("%s_%s.wav", jobID, safeName))

	totalBytes, err := deps.SaveUploadWithLimit(fileHeader, tempPath, int64(deps.MaxUploadSizeMB)*1024*1024)
	if err != nil {
		_ = os.Remove(tempPath)
		if deps.IsUploadTooLarge(err) {
			deps.Errf("upload.save", err, "file=%s too large", originalFilename)
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, fmt.Sprintf("업로드 용량 초과(%dMB)", deps.MaxUploadSizeMB))
		}
		deps.Errf("upload.save", err, "file=%s", originalFilename)
		return echo.NewHTTPError(http.StatusInternalServerError, "파일 저장 실패")
	}
	deps.UploadBytesAdd(float64(totalBytes))

	if err := deps.ConvertToWav(tempPath, wavPath); err != nil {
		_ = os.Remove(tempPath)
		deps.Errf("upload.convertToWav", err, "job_id=%s src=%s dst=%s", jobID, tempPath, wavPath)
		return echo.NewHTTPError(http.StatusInternalServerError, "ffmpeg 변환 실패")
	}
	_ = os.Remove(tempPath)

	wavBytes, err := os.ReadFile(wavPath)
	if err != nil {
		_ = os.Remove(wavPath)
		deps.Errf("upload.readWav", err, "job_id=%s path=%s", jobID, wavPath)
		return echo.NewHTTPError(http.StatusInternalServerError, "업로드 파일 처리 실패")
	}

	duration := deps.GetMediaDuration(wavPath)
	now := time.Now()
	job := &model.Job{
		Status:               deps.StatusPending,
		Filename:             inputName,
		UploadedAt:           now.Format("2006-01-02 15:04:05"),
		UploadedTS:           float64(now.Unix()),
		MediaDuration:        deps.FormatSecondsPtr(duration),
		MediaDurationSeconds: duration,
		RefineEnabled:        refineEnabled,
		OwnerID:              u.ID,
		Tags:                 validatedTags,
		FolderID:             folderID,
		IsTrashed:            false,
	}
	if description != "" {
		job.Description = description
	}

	deps.AddJob(jobID, job)
	if err := store.TouchFolderAndAncestors(u.ID, folderID); err != nil {
		deps.Errf("upload.touchFolder", err, "owner_id=%s folder_id=%s job_id=%s", u.ID, folderID, jobID)
	}

	if err := store.SaveJobBlob(jobID, store.BlobKindWav, wavBytes); err != nil {
		_ = os.Remove(wavPath)
		deps.Errf("upload.saveWavBlob", err, "job_id=%s", jobID)
		deps.DeleteJobs([]string{jobID})
		return echo.NewHTTPError(http.StatusInternalServerError, "업로드 파일 저장 실패")
	}
	_ = os.Remove(wavPath)

	deps.EnqueueTranscribe(jobID)
	deps.Logf("[UPLOAD] queued job_id=%s filename=%s bytes=%d", jobID, inputName, totalBytes)
	return c.Redirect(http.StatusSeeOther, routes.Job(jobID))
}
