package httptransport

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"whisperserver/src/internal/service"
)

// toEchoHTTPError converts service-layer errors into Echo HTTP errors.
func toEchoHTTPError(err error, fallbackStatus int, fallbackMessage string) error {
	if err == nil {
		return nil
	}
	var httpErr *service.HTTPError
	if errors.As(err, &httpErr) && httpErr != nil {
		return echo.NewHTTPError(httpErr.Status, httpErr.Message)
	}
	if fallbackStatus == 0 {
		fallbackStatus = http.StatusInternalServerError
	}
	if fallbackMessage == "" {
		fallbackMessage = "요청 처리 실패"
	}
	return echo.NewHTTPError(fallbackStatus, fallbackMessage)
}
