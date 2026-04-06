package httptransport

import (
	"errors"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
)

// SPAHandlers serves the built frontend and keeps legacy URLs redirecting to SPA routes.
type SPAHandlers struct {
	SPAIndexPath string

	// CurrentUser should return nil error when authenticated.
	CurrentUser func(echo.Context) error
}

// RootRedirectHandler chooses the default landing page based on auth state.
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

// SPAIndexHandler serves the built frontend entrypoint for client-side routing.
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

// SPALoginPageHandler redirects the legacy login route to the SPA route.
func SPALoginPageHandler(c echo.Context) error { return c.Redirect(http.StatusSeeOther, "/auth/login") }

// SPASignupPageHandler redirects the legacy signup route to the SPA route.
func SPASignupPageHandler(c echo.Context) error { return c.Redirect(http.StatusSeeOther, "/auth/join") }

// RedirectFilesToHomeHandler keeps the old files root pointing at the SPA home view.
func RedirectFilesToHomeHandler(c echo.Context) error {
	return c.Redirect(http.StatusMovedPermanently, "/files/home")
}

// RedirectJobsToRootHandler keeps the deprecated jobs page pointing at the files view.
func RedirectJobsToRootHandler(c echo.Context) error {
	return c.Redirect(http.StatusMovedPermanently, "/files/home")
}

// SPAUploadPageHandler collapses the old upload page into the files flow.
func SPAUploadPageHandler(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/files/root")
}

// SPATagsPageHandler preserves the old tags page entrypoint.
func SPATagsPageHandler(c echo.Context) error { return c.Redirect(http.StatusSeeOther, "/files/home") }

// SPAJobPageHandler rewrites legacy job URLs into the SPA detail route.
func SPAJobPageHandler(c echo.Context) error {
	target := "/file/" + c.Param("job_id")
	if raw := c.QueryString(); raw != "" {
		target += "?" + raw
	}
	return c.Redirect(http.StatusSeeOther, target)
}

// LegacyFilesPageRedirectHandler maps legacy folder routes into current SPA routes.
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

// LegacyTrashRedirectHandler maps the old trash page route into the SPA route.
func LegacyTrashRedirectHandler(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/files/trash")
}

// LegacyTagsRedirectHandler maps the old tags page route into the SPA route.
func LegacyTagsRedirectHandler(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/files/home")
}

// SPAFilesPageHandler forwards files routes to the shared SPA entrypoint.
func SPAFilesPageHandler(spaIndex echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error { return spaIndex(c) }
}

// SPATrashPageHandler forwards trash routes to the shared SPA entrypoint.
func SPATrashPageHandler(spaIndex echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error { return spaIndex(c) }
}
