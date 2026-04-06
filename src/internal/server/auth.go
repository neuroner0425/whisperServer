// auth.go keeps the server package's thin bridge to the auth runtime.
package server

import (
	"github.com/labstack/echo/v4"
	intauth "whisperserver/src/internal/auth"
)

type AuthUser = intauth.User

var authRuntime *intauth.Runtime

var (
	jwtSecret        []byte
	jwtIssuer        string
	jwtExpiryHours   int
	authCookieSecure bool
)

// initAuthRuntime builds the shared auth runtime after config has been loaded.
func initAuthRuntime() {
	authRuntime = intauth.New(intauth.Config{
		JWTSecret:        jwtSecret,
		JWTIssuer:        jwtIssuer,
		JWTExpiryHours:   jwtExpiryHours,
		AuthCookieSecure: authCookieSecure,
	}, procLogf, procErrf)
}

// currentUser is the server-local convenience wrapper used by HTTP wiring.
func currentUser(c echo.Context) (*AuthUser, error) {
	return authRuntime.CurrentUser(c)
}
