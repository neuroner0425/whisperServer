package httptransport

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

type User struct {
	ID      string
	LoginID string
	Email   string
}

type MeHandlers struct {
	// CurrentUserOrUnauthorized should write 401 JSON on failure.
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
}

func (h MeHandlers) Handler() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}
		return c.JSON(http.StatusOK, map[string]any{
			"user": map[string]string{
				"id":          u.ID,
				"login_id":    u.LoginID,
				"email":       u.Email,
				"displayName": displayName(*u),
			},
		})
	}
}

func displayName(u User) string {
	if strings.TrimSpace(u.LoginID) != "" {
		return u.LoginID
	}
	if idx := strings.Index(u.Email, "@"); idx > 0 {
		return u.Email[:idx]
	}
	return u.Email
}
