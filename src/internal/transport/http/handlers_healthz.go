package httptransport

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// HealthzHandler reports that the HTTP process is alive.
func HealthzHandler(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
