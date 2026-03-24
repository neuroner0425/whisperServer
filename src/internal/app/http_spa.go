package app

import (
	"errors"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
)

func spaIndexHandler(c echo.Context) error {
	if _, err := os.Stat(spaIndexPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c.String(http.StatusServiceUnavailable, "SPA build not found. Run `npm install && npm run build` in ./frontend first.")
		}
		return c.String(http.StatusInternalServerError, "Failed to load SPA build.")
	}
	return c.File(spaIndexPath)
}
