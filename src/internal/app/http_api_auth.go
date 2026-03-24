package app

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func apiMeJSONHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"user": map[string]string{
			"id":          u.ID,
			"login_id":    u.LoginID,
			"email":       u.Email,
			"displayName": currentUserName(c),
		},
	})
}
