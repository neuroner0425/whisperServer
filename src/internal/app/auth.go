package app

import (
	"crypto/rand"

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

func authMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return authHandlers.Middleware(next)
}

func currentUser(c echo.Context) (*AuthUser, error) {
	return authHandlers.CurrentUser(c)
}

func loginGetHandler(c echo.Context) error {
	return authHandlers.LoginGetHandler(c)
}

func signupGetHandler(c echo.Context) error {
	return authHandlers.SignupGetHandler(c)
}

func signupPostHandler(c echo.Context) error {
	return authHandlers.SignupPostHandler(c)
}

func loginPostHandler(c echo.Context) error {
	return authHandlers.LoginPostHandler(c)
}

func logoutPostHandler(c echo.Context) error {
	return authHandlers.LogoutPostHandler(c)
}
