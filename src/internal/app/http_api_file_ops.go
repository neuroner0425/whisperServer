package app

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/store"
)

func apiBatchMoveJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	var body struct {
		JobIDs         []string `json:"job_ids"`
		FolderIDs      []string `json:"folder_ids"`
		TargetFolderID string   `json:"target_folder_id"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	targetFolder := normalizeFolderID(body.TargetFolderID)
	if targetFolder != "" {
		f, err := store.GetFolderByID(u.ID, targetFolder)
		if err != nil || f.IsTrashed {
			return echo.NewHTTPError(http.StatusBadRequest, "유효하지 않은 대상 폴더입니다.")
		}
	}
	touchedFolders := map[string]struct{}{}
	for _, id := range body.JobIDs {
		job := getJob(id)
		if job != nil && job.OwnerID == u.ID && !isJobTrashed(job) {
			if job.FolderID != "" {
				touchedFolders[job.FolderID] = struct{}{}
			}
			if targetFolder != "" {
				touchedFolders[targetFolder] = struct{}{}
			}
			setJobFields(id, map[string]any{"folder_id": targetFolder})
		}
	}
	for _, id := range body.FolderIDs {
		id = normalizeFolderID(id)
		if id == "" || id == targetFolder {
			continue
		}
		f, err := store.GetFolderByID(u.ID, id)
		if err != nil || f.IsTrashed {
			continue
		}
		if targetFolder != "" {
			descendant, err := store.IsFolderDescendant(u.ID, id, targetFolder)
			if err != nil || descendant {
				continue
			}
		}
		if err := store.MoveFolder(u.ID, id, targetFolder); err != nil {
			continue
		}
		if f.ParentID != "" {
			touchedFolders[f.ParentID] = struct{}{}
		}
		if targetFolder != "" {
			touchedFolders[targetFolder] = struct{}{}
		}
	}
	for id := range touchedFolders {
		_ = store.TouchFolderAndAncestors(u.ID, id)
	}
	eventBroker.Notify(u.ID, "files.changed", nil)
	return c.JSON(http.StatusOK, map[string]any{
		"target_folder_id": targetFolder,
		"job_ids":          body.JobIDs,
		"folder_ids":       body.FolderIDs,
	})
}

func downloadFolderJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	folderID := normalizeFolderID(c.Param("folder_id"))
	if folderID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더를 찾을 수 없습니다.")
	}
	folder, err := store.GetFolderByID(u.ID, folderID)
	if err != nil || folder.IsTrashed {
		return echo.NewHTTPError(http.StatusNotFound, "폴더를 찾을 수 없습니다.")
	}

	subtree := collectFolderSubtree(u.ID, []string{folderID}, false)
	subtree[folderID] = struct{}{}
	snapshot := jobsSnapshot()
	buf := bytes.NewBuffer(nil)
	zw := zip.NewWriter(buf)
	added := 0
	for id, job := range snapshot {
		if job.OwnerID != u.ID || job.IsTrashed || job.Status != statusCompleted {
			continue
		}
		if _, ok := subtree[normalizeFolderID(job.FolderID)]; !ok {
			continue
		}
		blobKind := store.BlobKindTranscript
		suffix := ".txt"
		if store.HasJobBlob(id, store.BlobKindRefined) {
			blobKind = store.BlobKindRefined
			suffix = "_refined.txt"
		}
		b, err := store.LoadJobBlob(id, blobKind)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(job.Filename, filepath.Ext(job.Filename))
		w, err := zw.Create(base + suffix)
		if err != nil {
			continue
		}
		if _, err := w.Write(b); err != nil {
			continue
		}
		added++
	}
	_ = zw.Close()
	if added == 0 {
		return echo.NewHTTPError(http.StatusNotFound, "다운로드 가능한 결과가 없습니다.")
	}
	zipName := fmt.Sprintf("%s_%s.zip", folder.Name, time.Now().Format("20060102_150405"))
	c.Response().Header().Set(echo.HeaderContentDisposition, fmt.Sprintf(`attachment; filename="%s"`, zipName))
	return c.Blob(http.StatusOK, "application/zip", buf.Bytes())
}
