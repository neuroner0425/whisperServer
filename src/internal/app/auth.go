package app

import (
	"crypto/rand"
	"net/http"

	"github.com/labstack/echo/v4"
	httpx "whisperserver/src/internal/http"
)

type AuthUser = httpx.User

var authHandlers *httpx.Auth

var (
	jwtSecret        []byte
	jwtIssuer        string
	jwtExpiryHours   int
	authCookieSecure bool
)

func initAuthSecret() {
	if len(jwtSecret) != 0 {
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("failed to initialize jwt secret")
	}
	jwtSecret = b
	procLogf("[AUTH] JWT_SECRET not set, using ephemeral secret")
}

func initAuthHandlers() {
	authHandlers = httpx.NewAuth(jwtSecret, jwtIssuer, jwtExpiryHours, authCookieSecure, procLogf, procErrf)
}

func currentUser(c echo.Context) (*AuthUser, error) {
	return authHandlers.CurrentUser(c)
}

func apiMeJSONHandler(c echo.Context) error {
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil
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
