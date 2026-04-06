package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

type TagsHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	TagSvc                    *service.TagService

	IsValidTagName         func(string) bool
	RemoveTagFromOwnerJobs func(ownerID, tagName string)

	GetJob       func(string) *model.Job
	SetJobFields func(string, map[string]any)

	Logf func(string, ...any)
	Errf func(string, error, string, ...any)
}

func (h TagsHandlers) List() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.TagSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		tags, err := h.TagSvc.List(u.ID)
		if err != nil {
			if h.Errf != nil {
				h.Errf("tags.list", err, "owner_id=%s", u.ID)
			}
			return toEchoHTTPError(err, http.StatusInternalServerError, "태그 조회 실패")
		}
		return c.JSON(http.StatusOK, map[string]any{"tags": tags})
	}
}

func (h TagsHandlers) Create() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.TagSvc == nil || h.IsValidTagName == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
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
		if err := h.TagSvc.Upsert(u.ID, name, desc, h.IsValidTagName); err != nil {
			if h.Errf != nil {
				h.Errf("tag.upsert", err, "owner_id=%s name=%s", u.ID, name)
			}
			return toEchoHTTPError(err, http.StatusInternalServerError, "태그 저장 실패")
		}
		if h.Logf != nil {
			h.Logf("[TAG] upsert owner_id=%s name=%s", u.ID, name)
		}
		return c.JSON(http.StatusOK, map[string]string{"name": name, "description": desc})
	}
}

func (h TagsHandlers) Delete() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.TagSvc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		name := strings.TrimSpace(c.Param("name"))
		if err := h.TagSvc.Delete(u.ID, name); err != nil {
			if h.Errf != nil {
				h.Errf("tag.delete", err, "owner_id=%s name=%s", u.ID, name)
			}
			return toEchoHTTPError(err, http.StatusInternalServerError, "태그 삭제 실패")
		}
		if h.RemoveTagFromOwnerJobs != nil {
			h.RemoveTagFromOwnerJobs(u.ID, name)
		}
		if h.Logf != nil {
			h.Logf("[TAG] delete owner_id=%s name=%s", u.ID, name)
		}
		return c.JSON(http.StatusOK, map[string]string{"name": name, "status": "deleted"})
	}
}

func (h TagsHandlers) UpdateJobTags() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.TagSvc == nil || h.GetJob == nil || h.SetJobFields == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		jobID := strings.TrimSpace(c.Param("job_id"))
		job := h.GetJob(jobID)
		if job == nil || job.OwnerID != u.ID {
			return echo.NewHTTPError(http.StatusNotFound, "작업을 찾을 수 없습니다.")
		}

		var body struct {
			Tags []string `json:"tags"`
		}
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "잘못된 요청입니다.")
		}

		validated, err := h.TagSvc.ValidateOwnedTags(u.ID, body.Tags)
		if err != nil {
			if h.Errf != nil {
				h.Errf("tag.listNames", err, "owner_id=%s", u.ID)
			}
			return toEchoHTTPError(err, http.StatusInternalServerError, "태그 조회 실패")
		}

		h.SetJobFields(jobID, map[string]any{"tags": validated})
		if h.Logf != nil {
			h.Logf("[TAG] job update job_id=%s owner_id=%s tags=%s", jobID, u.ID, strings.Join(validated, ","))
		}
		return c.JSON(http.StatusOK, map[string]any{"job_id": jobID, "tags": validated})
	}
}
