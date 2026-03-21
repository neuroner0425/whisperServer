package app

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

func apiUploadJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "파일이 없습니다.")
	}
	jobID, filename, err := createUploadedJob(c, u.ID, fileHeader)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]string{
		"job_id":   jobID,
		"filename": filename,
		"job_url":  routes.Job(jobID),
	})
}

func createUploadedJob(c echo.Context, ownerID string, fileHeader *multipart.FileHeader) (string, string, error) {
	if fileHeader.Filename == "" {
		return "", "", echo.NewHTTPError(http.StatusBadRequest, "파일을 선택하세요.")
	}
	if !uploadDeps().AllowedFile(fileHeader.Filename) {
		return "", "", echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("허용되지 않는 파일 형식입니다. 허용: %s", strings.Join(uploadDeps().SortedExts(), ", ")))
	}
	if ct := fileHeader.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "audio/") {
		return "", "", echo.NewHTTPError(http.StatusBadRequest, "현재는 오디오 파일만 업로드할 수 있습니다.")
	}
	if uploadDeps().DetectFileType(fileHeader.Filename) != "audio" {
		return "", "", echo.NewHTTPError(http.StatusBadRequest, "현재는 오디오 파일만 업로드할 수 있습니다.")
	}

	inputName := c.FormValue("display_name")
	description := strings.TrimSpace(c.FormValue("description"))
	selectedTags := parseSelectedTags(c)
	if singleTag := c.FormValue("tag"); singleTag != "" {
		selectedTags = append(selectedTags, singleTag)
	}
	folderID := normalizeFolderID(c.FormValue("folder_id"))
	allowedTags, err := store.ListTagNamesByOwner(ownerID)
	if err != nil {
		return "", "", echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}

	validatedTags := make([]string, 0, len(selectedTags))
	for _, t := range selectedTags {
		if _, ok := allowedTags[t]; ok {
			validatedTags = append(validatedTags, t)
		}
	}
	refineEnabled := uploadDeps().Truthy(c.FormValue("refine"))
	if folderID != "" {
		f, err := store.GetFolderByID(ownerID, folderID)
		if err != nil || f.IsTrashed {
			return "", "", echo.NewHTTPError(http.StatusBadRequest, "유효하지 않은 폴더입니다.")
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

	jobID := uuid.NewString()
	tempPath := filepath.Join(tmpFolder, fmt.Sprintf("%s_temp%s", jobID, ext))
	wavPath := tempWavPath(jobID)

	totalBytes, err := uploadDeps().SaveUploadWithLimit(fileHeader, tempPath, int64(maxUploadSizeMB)*1024*1024, int64(uploadRateLimitKB)*1024)
	if err != nil {
		_ = os.Remove(tempPath)
		if uploadDeps().IsUploadTooLarge(err) {
			return "", "", echo.NewHTTPError(http.StatusRequestEntityTooLarge, fmt.Sprintf("업로드 용량 초과(%dMB)", maxUploadSizeMB))
		}
		return "", "", echo.NewHTTPError(http.StatusInternalServerError, "파일 저장 실패")
	}
	uploadBytes.Add(float64(totalBytes))

	if err := uploadDeps().ConvertToWav(tempPath, wavPath); err != nil {
		_ = os.Remove(tempPath)
		return "", "", echo.NewHTTPError(http.StatusInternalServerError, "ffmpeg 변환 실패")
	}
	_ = os.Remove(tempPath)

	duration := uploadDeps().GetMediaDuration(wavPath)
	now := time.Now()
	job := &model.Job{
		Status:               statusPending,
		Filename:             inputName,
		FileType:             uploadDeps().DetectFileType(originalFilename),
		UploadedAt:           now.Format("2006-01-02 15:04:05"),
		UploadedTS:           float64(now.Unix()),
		MediaDuration:        uploadDeps().FormatSecondsPtr(duration),
		MediaDurationSeconds: duration,
		RefineEnabled:        refineEnabled,
		OwnerID:              ownerID,
		Tags:                 validatedTags,
		FolderID:             folderID,
		IsTrashed:            false,
	}
	if description != "" {
		job.Description = description
	}

	addJob(jobID, job)
	if err := store.TouchFolderAndAncestors(ownerID, folderID); err != nil {
		procErrf("api.upload.touchFolder", err, "owner_id=%s folder_id=%s job_id=%s", ownerID, folderID, jobID)
	}
	enqueueTranscribe(jobID)
	return jobID, inputName, nil
}
