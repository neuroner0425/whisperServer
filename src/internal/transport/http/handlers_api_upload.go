package httptransport

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/service"
)

type UploadHandlers struct {
	// Auth
	CurrentUser               func(echo.Context) (*User, bool) // for HTML redirects
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool) // for JSON 401 (should write 401 JSON)

	// Form parsing helpers
	ParseSelectedTags func(echo.Context) []string
	NormalizeFolderID func(string) string
	Truthy            func(string) bool

	Svc *service.UploadService
}

func (h UploadHandlers) PostHTML() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser == nil || h.Svc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUser(c)
		if !ok || u == nil {
			return c.Redirect(http.StatusSeeOther, routes.Login)
		}

		fileHeader, err := c.FormFile("file")
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "파일이 없습니다.")
		}

		jobID, _, err := h.Svc.Create(service.UploadCreateRequest{
			OwnerID:          u.ID,
			DisplayName:      c.FormValue("display_name"),
			Description:      c.FormValue("description"),
			ClientUploadID:   c.FormValue("client_upload_id"),
			FolderID:         h.normalizeFolderID(c.FormValue("folder_id")),
			RefineRequested:  h.truthy(c.FormValue("refine")),
			SelectedTags:     h.parseSelectedTags(c),
			SingleTag:        c.FormValue("tag"),
			FileHeader:       fileHeader,
		})
		if err != nil {
			return h.toEchoError(err)
		}
		return c.Redirect(http.StatusSeeOther, routes.Job(jobID))
	}
}

func (h UploadHandlers) PostJSON() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil || h.Svc == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			// CurrentUserOrUnauthorized is responsible for writing 401 JSON.
			return nil
		}

		fileHeader, err := c.FormFile("file")
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "파일이 없습니다.")
		}

		jobID, filename, err := h.Svc.Create(service.UploadCreateRequest{
			OwnerID:          u.ID,
			DisplayName:      c.FormValue("display_name"),
			Description:      c.FormValue("description"),
			ClientUploadID:   c.FormValue("client_upload_id"),
			FolderID:         h.normalizeFolderID(c.FormValue("folder_id")),
			RefineRequested:  h.truthy(c.FormValue("refine")),
			SelectedTags:     h.parseSelectedTags(c),
			SingleTag:        c.FormValue("tag"),
			FileHeader:       fileHeader,
		})
		if err != nil {
			return h.toEchoError(err)
		}

		clientUploadID := strings.TrimSpace(c.FormValue("client_upload_id"))
		return c.JSON(http.StatusOK, map[string]string{
			"job_id":           jobID,
			"filename":         filename,
			"job_url":          routes.Job(jobID),
			"client_upload_id": clientUploadID,
		})
	}
}

func (h UploadHandlers) parseSelectedTags(c echo.Context) []string {
	if h.ParseSelectedTags == nil {
		return nil
	}
	return h.ParseSelectedTags(c)
}

func (h UploadHandlers) truthy(v string) bool {
	if h.Truthy != nil {
		return h.Truthy(v)
	}
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "1" || v == "true" || v == "on" || v == "yes" || v == "y"
}

func (h UploadHandlers) normalizeFolderID(v string) string {
	if h.NormalizeFolderID != nil {
		return h.NormalizeFolderID(v)
	}
	return strings.TrimSpace(v)
}

func (h UploadHandlers) toEchoError(err error) error {
	var httpErr *service.HTTPError
	if errors.As(err, &httpErr) && httpErr != nil {
		return echo.NewHTTPError(httpErr.Status, httpErr.Message)
	}
	return echo.NewHTTPError(http.StatusInternalServerError, "업로드 처리 실패")
}

