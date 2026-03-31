package httpx

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

type TagDeps struct {
	CurrentUser            func(echo.Context) (*User, error)
	CurrentUserName        func(echo.Context) string
	GetJob                 func(string) *model.Job
	SetJobFields           func(string, map[string]any)
	ParseSelectedTags      func(echo.Context) []string
	IsValidTagName         func(string) bool
	RemoveTagFromOwnerJobs func(string, string)
	Logf                   func(string, ...any)
	Errf                   func(string, error, string, ...any)
}

func CreateTagHandler(c echo.Context, deps TagDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	name := strings.TrimSpace(c.FormValue("tag_name"))
	desc := strings.TrimSpace(c.FormValue("tag_description"))
	next := strings.TrimSpace(c.FormValue("next"))
	if next == "" {
		next = routes.Tags
	}
	if !strings.HasPrefix(next, "/") {
		next = routes.Upload
	}
	if !deps.IsValidTagName(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "태그명은 공백 없이 문자/숫자/_ 만 사용할 수 있습니다.")
	}
	if desc == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "태그 설명을 입력하세요.")
	}
	if err := store.UpsertTag(u.ID, name, desc); err != nil {
		deps.Errf("tag.upsert", err, "owner_id=%s name=%s", u.ID, name)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 저장 실패")
	}
	deps.Logf("[TAG] upsert owner_id=%s name=%s", u.ID, name)
	return c.Redirect(http.StatusSeeOther, next)
}

func TagsPageHandler(c echo.Context, deps TagDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	tags, err := store.ListTagsByOwner(u.ID)
	if err != nil {
		deps.Errf("tags.list", err, "owner_id=%s", u.ID)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	return c.Render(http.StatusOK, "tags_index.html", map[string]any{
		"CurrentUserName": deps.CurrentUserName(c),
		"Tags":            tags,
	})
}

func TagsJSONHandler(c echo.Context, deps TagDeps) error {
	u, err := CurrentUserOrUnauthorizedJSON(c, deps.CurrentUser)
	if err != nil {
		return nil
	}
	tags, err := store.ListTagsByOwner(u.ID)
	if err != nil {
		deps.Errf("tags.list", err, "owner_id=%s", u.ID)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	return c.JSON(http.StatusOK, map[string]any{"tags": tags})
}

func DeleteTagHandler(c echo.Context, deps TagDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	name := strings.TrimSpace(c.FormValue("tag_name"))
	if name == "" {
		return c.Redirect(http.StatusSeeOther, routes.Tags)
	}
	if err := store.DeleteTag(u.ID, name); err != nil {
		deps.Errf("tag.delete", err, "owner_id=%s name=%s", u.ID, name)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 삭제 실패")
	}
	deps.RemoveTagFromOwnerJobs(u.ID, name)
	deps.Logf("[TAG] delete owner_id=%s name=%s", u.ID, name)
	return c.Redirect(http.StatusSeeOther, routes.Tags)
}

func CreateTagJSONHandler(c echo.Context, deps TagDeps) error {
	u, err := CurrentUserOrUnauthorizedJSON(c, deps.CurrentUser)
	if err != nil {
		return nil
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	name := strings.TrimSpace(body.Name)
	desc := strings.TrimSpace(body.Description)
	if !deps.IsValidTagName(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "태그명은 공백 없이 문자/숫자/_ 만 사용할 수 있습니다.")
	}
	if desc == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "태그 설명을 입력하세요.")
	}
	if err := store.UpsertTag(u.ID, name, desc); err != nil {
		deps.Errf("tag.upsert", err, "owner_id=%s name=%s", u.ID, name)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 저장 실패")
	}
	deps.Logf("[TAG] upsert owner_id=%s name=%s", u.ID, name)
	return c.JSON(http.StatusOK, map[string]string{"name": name, "description": desc})
}

func DeleteTagJSONHandler(c echo.Context, deps TagDeps) error {
	u, err := CurrentUserOrUnauthorizedJSON(c, deps.CurrentUser)
	if err != nil {
		return nil
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "삭제할 태그가 없습니다.")
	}
	if err := store.DeleteTag(u.ID, name); err != nil {
		deps.Errf("tag.delete", err, "owner_id=%s name=%s", u.ID, name)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 삭제 실패")
	}
	deps.RemoveTagFromOwnerJobs(u.ID, name)
	deps.Logf("[TAG] delete owner_id=%s name=%s", u.ID, name)
	return c.JSON(http.StatusOK, map[string]string{"name": name, "status": "deleted"})
}

func UpdateJobTagsHandler(c echo.Context, deps TagDeps) error {
	u, err := deps.CurrentUser(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, routes.Login)
	}
	jobID := c.Param("job_id")
	job := deps.GetJob(jobID)
	if job == nil || job.OwnerID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}

	selected := deps.ParseSelectedTags(c)
	validated, err := ValidateOwnedTags(u.ID, selected)
	if err != nil {
		deps.Errf("tag.listNames", err, "owner_id=%s", u.ID)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	deps.SetJobFields(jobID, map[string]any{"tags": validated})
	deps.Logf("[TAG] job update job_id=%s owner_id=%s tags=%s", jobID, u.ID, strings.Join(validated, ","))
	return c.Redirect(http.StatusSeeOther, routes.Job(jobID))
}

func UpdateJobTagsJSONHandler(c echo.Context, deps TagDeps) error {
	u, err := CurrentUserOrUnauthorizedJSON(c, deps.CurrentUser)
	if err != nil {
		return nil
	}
	jobID := c.Param("job_id")
	job := deps.GetJob(jobID)
	if job == nil || job.OwnerID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
	}
	validated, err := ValidateOwnedTags(u.ID, body.Tags)
	if err != nil {
		deps.Errf("tag.listNames", err, "owner_id=%s", u.ID)
		return echo.NewHTTPError(http.StatusInternalServerError, "태그 조회 실패")
	}
	deps.SetJobFields(jobID, map[string]any{"tags": validated})
	deps.Logf("[TAG] job update job_id=%s owner_id=%s tags=%s", jobID, u.ID, strings.Join(validated, ","))
	return c.JSON(http.StatusOK, map[string]any{"job_id": jobID, "tags": validated})
}
