package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

// LegacyMutationHandlers serves legacy HTML form mutation endpoints still referenced by older clients:
// - POST /batch-delete
// - POST /batch-move
// - POST /tags, /tags/delete
// - POST /folders + folder mutations
// - POST /job/:job_id/* mutations
//
// It intentionally keeps redirect-based flows.
type LegacyMutationHandlers struct {
	CurrentUser func(echo.Context) (*User, bool)

	GetJob         func(string) *model.Job
	SetJobFields   func(string, map[string]any)
	MarkJobTrashed func(string)

	FolderSvc *service.FolderService
	BlobSvc   *service.JobBlobService
	TagSvc    *service.TagService

	CollectFolderSubtree   func(userID string, folderIDs []string, trashFolders bool) map[string]struct{}
	MarkSubtreeJobsTrashed func(userID string, subtree map[string]struct{})

	EnqueueTranscribe func(string)
	EnqueueRefine     func(string)
	EnqueuePDFExtract func(string)

	StatusPending         string
	StatusRefiningPending string

	IsValidTagName         func(string) bool
	RemoveTagFromOwnerJobs func(ownerID, tagName string)

	Logf func(string, ...any)
	Errf func(string, error, string, ...any)
}

func (h LegacyMutationHandlers) BatchDelete() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.GetJob == nil || h.SetJobFields == nil || h.MarkJobTrashed == nil || h.FolderSvc == nil || h.CollectFolderSubtree == nil || h.MarkSubtreeJobsTrashed == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		if err := c.Request().ParseForm(); err != nil {
			if h.Errf != nil {
				h.Errf("legacy.batchDelete.parseForm", err, "request parse failed")
			}
			return c.Redirect(http.StatusSeeOther, filesHomePath)
		}
		jobIDs := c.Request().PostForm["job_ids"]
		folderIDs := c.Request().PostForm["folder_ids"]
		if len(jobIDs) == 0 && len(folderIDs) == 0 {
			if h.Logf != nil {
				h.Logf("[BATCH_DELETE] skipped reason=no selection")
			}
			return c.Redirect(http.StatusSeeOther, filesHomePath)
		}

		touchedFolders := map[string]struct{}{}
		ownedJobs := make([]string, 0, len(jobIDs))
		for _, id := range jobIDs {
			id = strings.TrimSpace(id)
			job := h.GetJob(id)
			if job != nil && job.OwnerID == u.ID && !job.IsTrashed {
				ownedJobs = append(ownedJobs, id)
				if strings.TrimSpace(job.FolderID) != "" {
					touchedFolders[job.FolderID] = struct{}{}
				}
			}
		}
		for _, id := range ownedJobs {
			h.MarkJobTrashed(id)
		}

		subtree := h.CollectFolderSubtree(u.ID, folderIDs, true)
		h.MarkSubtreeJobsTrashed(u.ID, subtree)

		for _, id := range folderIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			// folderIDs were just trashed; allowTrashed=true so we can still read parent id.
			f, err := h.FolderSvc.Require(u.ID, id, true, http.StatusBadRequest, "유효하지 않은 폴더입니다.")
			if err == nil && f != nil && strings.TrimSpace(f.ParentID) != "" {
				touchedFolders[f.ParentID] = struct{}{}
			}
		}

		for id := range touchedFolders {
			if err := h.FolderSvc.TouchAncestors(u.ID, id); err != nil && h.Errf != nil {
				h.Errf("legacy.batchDelete.touchFolder", err, "owner_id=%s folder_id=%s", u.ID, id)
			}
		}
		if h.Logf != nil {
			h.Logf("[BATCH_TRASH] success jobs=%d folders=%d subtree=%d", len(ownedJobs), len(folderIDs), len(subtree))
		}
		return c.Redirect(http.StatusSeeOther, filesHomePath)
	}
}

func (h LegacyMutationHandlers) BatchMove() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.FolderSvc == nil || h.GetJob == nil || h.SetJobFields == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		if err := c.Request().ParseForm(); err != nil {
			return c.Redirect(http.StatusSeeOther, filesHomePath)
		}
		returnTo := SafeReturnPath(c.FormValue("return_to"))
		targetFolder := strings.TrimSpace(c.FormValue("target_folder_id"))
		if targetFolder != "" {
			if _, err := h.FolderSvc.Require(u.ID, targetFolder, false, http.StatusBadRequest, "유효하지 않은 대상 폴더입니다."); err != nil {
				if h.Errf != nil {
					h.Errf("legacy.batchMove.invalidTarget", err, "owner_id=%s target_folder=%s", u.ID, targetFolder)
				}
				return c.Redirect(http.StatusSeeOther, returnTo)
			}
		}

		touchedFolders := map[string]struct{}{}
		for _, id := range c.Request().PostForm["job_ids"] {
			id = strings.TrimSpace(id)
			job := h.GetJob(id)
			if job != nil && job.OwnerID == u.ID && !job.IsTrashed {
				if strings.TrimSpace(job.FolderID) != "" {
					touchedFolders[job.FolderID] = struct{}{}
				}
				if targetFolder != "" {
					touchedFolders[targetFolder] = struct{}{}
				}
				h.SetJobFields(id, map[string]any{"folder_id": targetFolder})
			}
		}
		for _, id := range c.Request().PostForm["folder_ids"] {
			id = strings.TrimSpace(id)
			if id == "" || targetFolder == id {
				continue
			}
			f, err := h.FolderSvc.Require(u.ID, id, false, http.StatusBadRequest, "유효하지 않은 폴더입니다.")
			if err != nil {
				continue
			}
			if targetFolder != "" {
				descendant, err := h.FolderSvc.IsDescendant(u.ID, id, targetFolder)
				if err != nil || descendant {
					continue
				}
			}
			if err := h.FolderSvc.Move(u.ID, id, targetFolder); err != nil {
				if h.Errf != nil {
					h.Errf("legacy.batchMove.folder", err, "owner_id=%s folder_id=%s target=%s", u.ID, id, targetFolder)
				}
				continue
			}
			if f != nil && strings.TrimSpace(f.ParentID) != "" {
				touchedFolders[f.ParentID] = struct{}{}
			}
			if targetFolder != "" {
				touchedFolders[targetFolder] = struct{}{}
			}
		}

		for id := range touchedFolders {
			if err := h.FolderSvc.TouchAncestors(u.ID, id); err != nil && h.Errf != nil {
				h.Errf("legacy.batchMove.touchFolder", err, "owner_id=%s folder_id=%s target_folder_id=%s", u.ID, id, targetFolder)
			}
		}
		return c.Redirect(http.StatusSeeOther, returnTo)
	}
}

func (h LegacyMutationHandlers) CreateTagHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.TagSvc == nil || h.IsValidTagName == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		name := strings.TrimSpace(c.FormValue("tag_name"))
		desc := strings.TrimSpace(c.FormValue("tag_description"))
		next := strings.TrimSpace(c.FormValue("next"))
		if next == "" {
			next = tagsPath
		}
		if !strings.HasPrefix(next, "/") {
			next = uploadPath
		}
		if err := h.TagSvc.Upsert(u.ID, name, desc, h.IsValidTagName); err != nil {
			if h.Errf != nil {
				h.Errf("legacy.tag.upsert", err, "owner_id=%s name=%s", u.ID, name)
			}
			return toEchoHTTPError(err, http.StatusInternalServerError, "태그 저장 실패")
		}
		if h.Logf != nil {
			h.Logf("[TAG] upsert owner_id=%s name=%s", u.ID, name)
		}
		return c.Redirect(http.StatusSeeOther, next)
	}
}

func (h LegacyMutationHandlers) DeleteTagHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.TagSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		name := strings.TrimSpace(c.FormValue("tag_name"))
		if name == "" {
			return c.Redirect(http.StatusSeeOther, tagsPath)
		}
		if err := h.TagSvc.Delete(u.ID, name); err != nil {
			if h.Errf != nil {
				h.Errf("legacy.tag.delete", err, "owner_id=%s name=%s", u.ID, name)
			}
			return toEchoHTTPError(err, http.StatusInternalServerError, "태그 삭제 실패")
		}
		if h.RemoveTagFromOwnerJobs != nil {
			h.RemoveTagFromOwnerJobs(u.ID, name)
		}
		if h.Logf != nil {
			h.Logf("[TAG] delete owner_id=%s name=%s", u.ID, name)
		}
		return c.Redirect(http.StatusSeeOther, tagsPath)
	}
}

func (h LegacyMutationHandlers) CreateFolderHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.FolderSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		name := strings.TrimSpace(c.FormValue("folder_name"))
		parentID := strings.TrimSpace(c.FormValue("parent_id"))
		if parentID != "" {
			if _, err := h.FolderSvc.Require(u.ID, parentID, false, http.StatusBadRequest, "유효하지 않은 상위 폴더입니다."); err != nil {
				return toEchoHTTPError(err, http.StatusBadRequest, "유효하지 않은 상위 폴더입니다.")
			}
		}
		id, err := h.FolderSvc.Create(u.ID, name, parentID)
		if err != nil {
			if h.Errf != nil {
				h.Errf("legacy.folder.create", err, "owner_id=%s name=%s parent_id=%s", u.ID, name, parentID)
			}
			return toEchoHTTPError(err, http.StatusBadRequest, "폴더 생성 실패(중복 이름 확인)")
		}
		if h.Logf != nil {
			h.Logf("[FOLDER] create owner_id=%s id=%s name=%s parent_id=%s", u.ID, id, name, parentID)
		}
		if err := h.FolderSvc.TouchAncestors(u.ID, parentID); err != nil && h.Errf != nil {
			h.Errf("legacy.folder.createTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, id, parentID)
		}
		if parentID == "" {
			return c.Redirect(http.StatusSeeOther, filesRootPath)
		}
		return c.Redirect(http.StatusSeeOther, filesFolderPath(parentID))
	}
}

func (h LegacyMutationHandlers) RenameFolderHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.FolderSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		folderID := strings.TrimSpace(c.Param("folder_id"))
		newName := strings.TrimSpace(c.FormValue("new_name"))
		f, err := h.FolderSvc.Require(u.ID, folderID, false, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		if err != nil {
			return toEchoHTTPError(err, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		}
		if err := h.FolderSvc.Rename(u.ID, folderID, newName); err != nil {
			return toEchoHTTPError(err, http.StatusBadRequest, "폴더 이름 변경 실패(중복 이름 확인)")
		}
		parentID := ""
		if f != nil {
			parentID = strings.TrimSpace(f.ParentID)
		}
		if parentID == "" {
			return c.Redirect(http.StatusSeeOther, filesRootPath)
		}
		return c.Redirect(http.StatusSeeOther, filesFolderPath(parentID))
	}
}

func (h LegacyMutationHandlers) MoveFolderHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.FolderSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		folderID := strings.TrimSpace(c.Param("folder_id"))
		targetParent := strings.TrimSpace(c.FormValue("target_parent_id"))

		f, err := h.FolderSvc.Require(u.ID, folderID, false, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		if err != nil {
			return toEchoHTTPError(err, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
		}
		if targetParent == folderID {
			return echo.NewHTTPError(http.StatusBadRequest, "자기 자신으로 이동할 수 없습니다.")
		}
		if targetParent != "" {
			if _, err := h.FolderSvc.Require(u.ID, targetParent, false, http.StatusBadRequest, "유효하지 않은 대상 폴더입니다."); err != nil {
				return toEchoHTTPError(err, http.StatusBadRequest, "유효하지 않은 대상 폴더입니다.")
			}
			descendant, err := h.FolderSvc.IsDescendant(u.ID, folderID, targetParent)
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "폴더 이동 검증 실패")
			}
			if descendant {
				return echo.NewHTTPError(http.StatusBadRequest, "하위 폴더로 이동할 수 없습니다.")
			}
		}
		if err := h.FolderSvc.Move(u.ID, folderID, targetParent); err != nil {
			if h.Errf != nil {
				h.Errf("legacy.folder.move", err, "owner_id=%s folder_id=%s target_parent=%s", u.ID, folderID, targetParent)
			}
			return toEchoHTTPError(err, http.StatusBadRequest, "폴더 이동 실패")
		}
		if f != nil && strings.TrimSpace(f.ParentID) != "" {
			if err := h.FolderSvc.TouchAncestors(u.ID, f.ParentID); err != nil && h.Errf != nil {
				h.Errf("legacy.folder.moveTouchSourceParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
			}
		}
		if targetParent != "" {
			if err := h.FolderSvc.TouchAncestors(u.ID, targetParent); err != nil && h.Errf != nil {
				h.Errf("legacy.folder.moveTouchTargetParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, targetParent)
			}
		}
		if targetParent == "" {
			return c.Redirect(http.StatusSeeOther, filesRootPath)
		}
		return c.Redirect(http.StatusSeeOther, filesFolderPath(targetParent))
	}
}

func (h LegacyMutationHandlers) TrashFolderHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.FolderSvc == nil || h.CollectFolderSubtree == nil || h.MarkSubtreeJobsTrashed == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		folderID := strings.TrimSpace(c.Param("folder_id"))
		f, _ := h.FolderSvc.Require(u.ID, folderID, true, http.StatusBadRequest, "폴더 삭제 실패")
		subtree := h.CollectFolderSubtree(u.ID, []string{folderID}, false)
		if err := h.FolderSvc.Trash(u.ID, folderID); err != nil {
			return toEchoHTTPError(err, http.StatusBadRequest, "폴더 삭제 실패")
		}
		h.MarkSubtreeJobsTrashed(u.ID, subtree)
		if f != nil && strings.TrimSpace(f.ParentID) != "" {
			if err := h.FolderSvc.TouchAncestors(u.ID, f.ParentID); err != nil && h.Errf != nil {
				h.Errf("legacy.folder.trashTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
			}
		}
		return c.Redirect(http.StatusSeeOther, filesHomePath)
	}
}

func (h LegacyMutationHandlers) RestoreFolderHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.FolderSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		folderID := strings.TrimSpace(c.Param("folder_id"))
		f, err := h.FolderSvc.Restore(u.ID, folderID)
		if err != nil {
			return toEchoHTTPError(err, http.StatusBadRequest, "폴더 복구 실패")
		}
		if f != nil && strings.TrimSpace(f.ParentID) != "" {
			if err := h.FolderSvc.TouchAncestors(u.ID, f.ParentID); err != nil && h.Errf != nil {
				h.Errf("legacy.folder.restoreTouchParent", err, "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
			}
		}
		return c.Redirect(http.StatusSeeOther, trashPath)
	}
}

func (h LegacyMutationHandlers) TrashJobHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.FolderSvc == nil || h.GetJob == nil || h.MarkJobTrashed == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		jobID := strings.TrimSpace(c.Param("job_id"))
		job := h.GetJob(jobID)
		if job == nil || job.OwnerID != u.ID {
			return c.Redirect(http.StatusSeeOther, filesHomePath)
		}
		h.MarkJobTrashed(jobID)
		if err := h.FolderSvc.TouchAncestors(u.ID, job.FolderID); err != nil && h.Errf != nil {
			h.Errf("legacy.job.trashTouchFolder", err, "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, job.FolderID)
		}
		return c.Redirect(http.StatusSeeOther, filesHomePath)
	}
}

func (h LegacyMutationHandlers) RestoreJobHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.FolderSvc == nil || h.BlobSvc == nil || h.GetJob == nil || h.SetJobFields == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		jobID := strings.TrimSpace(c.Param("job_id"))
		job := h.GetJob(jobID)
		if job == nil || job.OwnerID != u.ID {
			return c.Redirect(http.StatusSeeOther, trashPath)
		}
		folderID := h.FolderSvc.EnsureRestored(u.ID, job.FolderID, h.Logf, h.Errf, "legacy.job.restore")
		h.SetJobFields(jobID, map[string]any{"is_trashed": false, "deleted_at": "", "folder_id": folderID})
		job = h.GetJob(jobID)
		service.ResumeRestoredJob(
			jobID,
			job,
			h.BlobSvc,
			h.SetJobFields,
			h.EnqueueTranscribe,
			h.EnqueueRefine,
			h.EnqueuePDFExtract,
			h.StatusPending,
			h.StatusRefiningPending,
		)
		if err := h.FolderSvc.TouchAncestors(u.ID, folderID); err != nil && h.Errf != nil {
			h.Errf("legacy.job.restoreTouchFolder", err, "owner_id=%s job_id=%s folder_id=%s", u.ID, jobID, folderID)
		}
		return c.Redirect(http.StatusSeeOther, trashPath)
	}
}

func (h LegacyMutationHandlers) RenameJobHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.GetJob == nil || h.SetJobFields == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		jobID := strings.TrimSpace(c.Param("job_id"))
		job := h.GetJob(jobID)
		if job == nil || job.OwnerID != u.ID || job.IsTrashed {
			return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
		}
		nextName := strings.TrimSpace(c.FormValue("new_name"))
		if nextName == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "새 파일명을 입력하세요.")
		}
		if strings.Contains(nextName, "/") || strings.Contains(nextName, `\`) {
			return echo.NewHTTPError(http.StatusBadRequest, "파일명에 경로 문자를 사용할 수 없습니다.")
		}
		h.SetJobFields(jobID, map[string]any{"filename": nextName})
		if h.Logf != nil {
			h.Logf("[JOB] rename owner_id=%s job_id=%s new_name=%s", u.ID, jobID, nextName)
		}
		return c.Redirect(http.StatusSeeOther, filesHomePath)
	}
}

func (h LegacyMutationHandlers) UpdateJobTagsHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.TagSvc == nil || h.GetJob == nil || h.SetJobFields == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, loginPath)
		}
		jobID := strings.TrimSpace(c.Param("job_id"))
		job := h.GetJob(jobID)
		if job == nil || job.OwnerID != u.ID {
			return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
		}

		selected := ParseSelectedTags(c, uniqueStringsKeepOrder)
		validated, err := h.TagSvc.ValidateOwnedTags(u.ID, selected)
		if err != nil {
			if h.Errf != nil {
				h.Errf("legacy.tag.listNames", err, "owner_id=%s", u.ID)
			}
			return toEchoHTTPError(err, http.StatusInternalServerError, "태그 조회 실패")
		}
		h.SetJobFields(jobID, map[string]any{"tags": validated})
		if h.Logf != nil {
			h.Logf("[TAG] job update job_id=%s owner_id=%s tags=%s", jobID, u.ID, strings.Join(validated, ","))
		}
		return c.Redirect(http.StatusSeeOther, jobPath(jobID))
	}
}

func uniqueStringsKeepOrder(v []string) []string {
	out := make([]string, 0, len(v))
	seen := map[string]struct{}{}
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
