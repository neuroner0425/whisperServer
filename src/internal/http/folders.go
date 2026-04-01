package httpx

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

type FolderDeps struct {
	CurrentUser       func(echo.Context) (*User, error)
	GetJob            func(string) *model.Job
	SetJobFields      func(string, map[string]any)
	IsJobTrashed      func(*model.Job) bool
	NormalizeFolderID func(string) string
	SafeReturnPath    func(string) string
	Logf              func(string, ...any)
	Errf              func(string, error, string, ...any)
}

func CreateFolderHandler(c echo.Context, deps FolderDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	name := strings.TrimSpace(c.FormValue("folder_name"))
	parentID := deps.NormalizeFolderID(c.FormValue("parent_id"))
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "폴더명을 입력하세요.")
	}
	if parentID != "" {
		if _, err := RequireFolderForOwner(u.ID, parentID, false, http.StatusBadRequest, "유효하지 않은 상위 폴더입니다."); err != nil {
			return err
		}
	}
	id, err := store.CreateFolder(u.ID, name, parentID)
	if err != nil {
		deps.Errf("folder.create", err, "owner_id=%s name=%s parent_id=%s", u.ID, name, parentID)
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 생성 실패(중복 이름 확인)")
	}
	deps.Logf("[FOLDER] create owner_id=%s id=%s name=%s parent_id=%s", u.ID, id, name, parentID)
	TouchFolderAncestors(u.ID, parentID, deps.Errf, "folder.createTouchParent", "owner_id=%s folder_id=%s parent_id=%s", u.ID, id, parentID)
	if parentID == "" {
		return c.Redirect(http.StatusSeeOther, routes.FilesRoot)
	}
	return c.Redirect(http.StatusSeeOther, routes.FilesFolder(parentID))
}

func MoveJobsHandler(c echo.Context, deps FolderDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	if err := c.Request().ParseForm(); err != nil {
		return c.Redirect(http.StatusSeeOther, routes.FilesHome)
	}
	returnTo := deps.SafeReturnPath(c.FormValue("return_to"))
	targetFolder := deps.NormalizeFolderID(c.FormValue("target_folder_id"))
	if targetFolder != "" {
		if _, err := RequireFolderForOwner(u.ID, targetFolder, false, http.StatusBadRequest, "유효하지 않은 대상 폴더입니다."); err != nil {
			deps.Errf("batchMove.invalidTarget", err, "owner_id=%s target_folder=%s", u.ID, targetFolder)
			return c.Redirect(http.StatusSeeOther, returnTo)
		}
	}

	touchedFolders := map[string]struct{}{}
	for _, id := range c.Request().PostForm["job_ids"] {
		job := deps.GetJob(id)
		if job != nil && job.OwnerID == u.ID && !deps.IsJobTrashed(job) {
			if job.FolderID != "" {
				touchedFolders[job.FolderID] = struct{}{}
			}
			if targetFolder != "" {
				touchedFolders[targetFolder] = struct{}{}
			}
			deps.SetJobFields(id, map[string]any{"folder_id": targetFolder})
		}
	}
	for _, id := range c.Request().PostForm["folder_ids"] {
		id = deps.NormalizeFolderID(id)
		if id == "" || targetFolder == id {
			continue
		}
		f, err := RequireFolderForOwner(u.ID, id, false, http.StatusBadRequest, "유효하지 않은 폴더입니다.")
		if err != nil {
			continue
		}
		if targetFolder != "" {
			descendant, err := store.IsFolderDescendant(u.ID, id, targetFolder)
			if err != nil || descendant {
				continue
			}
		}
		if err := store.MoveFolder(u.ID, id, targetFolder); err != nil {
			deps.Errf("batchMove.folder", err, "owner_id=%s folder_id=%s target=%s", u.ID, id, targetFolder)
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
		TouchFolderAncestors(u.ID, id, deps.Errf, "batchMove.touchFolder", "owner_id=%s folder_id=%s", u.ID, id)
	}
	return c.Redirect(http.StatusSeeOther, returnTo)
}

func RenameFolderHandler(c echo.Context, deps FolderDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	folderID := c.Param("folder_id")
	newName := strings.TrimSpace(c.FormValue("new_name"))
	if newName == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "새 폴더명을 입력하세요.")
	}
	f, err := RequireFolderForOwner(u.ID, folderID, false, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
	if err != nil {
		return err
	}
	if err := store.RenameFolder(u.ID, folderID, newName); err != nil {
		deps.Errf("folder.rename", err, "owner_id=%s folder_id=%s", u.ID, folderID)
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 이름 변경 실패(중복 이름 확인)")
	}
	parent := deps.NormalizeFolderID(f.ParentID)
	if parent == "" {
		return c.Redirect(http.StatusSeeOther, routes.FilesRoot)
	}
	return c.Redirect(http.StatusSeeOther, routes.FilesFolder(parent))
}

func MoveFolderHandler(c echo.Context, deps FolderDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	folderID := c.Param("folder_id")
	targetParent := deps.NormalizeFolderID(c.FormValue("target_parent_id"))

	f, err := RequireFolderForOwner(u.ID, folderID, false, http.StatusNotFound, "폴더를 찾을 수 없습니다.")
	if err != nil {
		return err
	}
	if targetParent == folderID {
		return echo.NewHTTPError(http.StatusBadRequest, "자기 자신으로 이동할 수 없습니다.")
	}
	if targetParent != "" {
		if _, err := RequireFolderForOwner(u.ID, targetParent, false, http.StatusBadRequest, "유효하지 않은 대상 폴더입니다."); err != nil {
			return err
		}
		descendant, err := store.IsFolderDescendant(u.ID, folderID, targetParent)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "폴더 이동 검증 실패")
		}
		if descendant {
			return echo.NewHTTPError(http.StatusBadRequest, "하위 폴더로 이동할 수 없습니다.")
		}
	}
	if err := store.MoveFolder(u.ID, folderID, targetParent); err != nil {
		deps.Errf("folder.move", err, "owner_id=%s folder_id=%s target_parent=%s", u.ID, folderID, targetParent)
		return echo.NewHTTPError(http.StatusBadRequest, "폴더 이동 실패")
	}
	TouchFolderAncestors(u.ID, f.ParentID, deps.Errf, "folder.moveTouchSourceParent", "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, f.ParentID)
	TouchFolderAncestors(u.ID, targetParent, deps.Errf, "folder.moveTouchTargetParent", "owner_id=%s folder_id=%s parent_id=%s", u.ID, folderID, targetParent)
	if targetParent == "" {
		return c.Redirect(http.StatusSeeOther, routes.FilesRoot)
	}
	return c.Redirect(http.StatusSeeOther, routes.FilesFolder(targetParent))
}
