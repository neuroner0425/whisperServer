package httptransport

import (
	"errors"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
)

type SPAHandlers struct {
	SPAIndexPath string

	// CurrentUser should return nil error when authenticated.
	CurrentUser func(echo.Context) error
}

func (h SPAHandlers) RootRedirectHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUser != nil {
			if err := h.CurrentUser(c); err == nil {
				return c.Redirect(http.StatusSeeOther, "/files/home")
			}
		}
		return c.Redirect(http.StatusSeeOther, "/auth/login")
	}
}

func (h SPAHandlers) SPAIndexHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		if _, err := os.Stat(h.SPAIndexPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return c.String(http.StatusServiceUnavailable, "SPA build not found. Run `npm install && npm run build` in ./frontend first.")
			}
			return c.String(http.StatusInternalServerError, "Failed to load SPA build.")
		}
		return c.File(h.SPAIndexPath)
	}
}

func SPALoginPageHandler(c echo.Context) error  { return c.Redirect(http.StatusSeeOther, "/auth/login") }
func SPASignupPageHandler(c echo.Context) error { return c.Redirect(http.StatusSeeOther, "/auth/join") }
func RedirectFilesToHomeHandler(c echo.Context) error {
	return c.Redirect(http.StatusMovedPermanently, "/files/home")
}
func RedirectJobsToRootHandler(c echo.Context) error {
	return c.Redirect(http.StatusMovedPermanently, "/files/home")
}
func SPAUploadPageHandler(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/files/root")
}
func SPATagsPageHandler(c echo.Context) error { return c.Redirect(http.StatusSeeOther, "/files/home") }

func SPAJobPageHandler(c echo.Context) error {
	target := "/file/" + c.Param("job_id")
	if raw := c.QueryString(); raw != "" {
		target += "?" + raw
	}
	return c.Redirect(http.StatusSeeOther, target)
}

func LegacyFilesPageRedirectHandler(c echo.Context) error {
	target := "/files/home"
	switch c.Path() {
	case "/files/root":
		target = "/files/root"
	case "/files/home":
		target = "/files/home"
	default:
		if folderID := c.Param("folder_id"); folderID != "" {
			target = "/files/folder/" + folderID
		}
	}
	if raw := c.QueryString(); raw != "" {
		target += "?" + raw
	}
	return c.Redirect(http.StatusSeeOther, target)
}

func LegacyTrashRedirectHandler(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/files/trash")
}
func LegacyTagsRedirectHandler(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/files/home")
}

func SPAFilesPageHandler(spaIndex echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error { return spaIndex(c) }
}

func SPATrashPageHandler(spaIndex echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error { return spaIndex(c) }
}
