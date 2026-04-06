// runtime.go wraps the auth core in the shape expected by server bootstrap.
package auth

import (
	"crypto/rand"

	"github.com/labstack/echo/v4"
)

type Config struct {
	JWTSecret        []byte
	JWTIssuer        string
	JWTExpiryHours   int
	AuthCookieSecure bool
}

type Runtime struct {
	handlers *Auth
}

// New builds the auth runtime and creates an ephemeral secret when none was configured.
func New(cfg Config, logf func(string, ...any), errf func(string, error, string, ...any)) *Runtime {
	secret := cfg.JWTSecret
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			panic("failed to initialize jwt secret")
		}
		if logf != nil {
			logf("[AUTH] JWT_SECRET not set, using ephemeral secret")
		}
	}
	return &Runtime{
		handlers: NewAuth(secret, cfg.JWTIssuer, cfg.JWTExpiryHours, cfg.AuthCookieSecure, logf, errf),
	}
}

// Middleware forwards the auth middleware into Echo setup code.
func (r *Runtime) Middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return r.handlers.Middleware(next)
}

// CurrentUser forwards current-user resolution into the auth core.
func (r *Runtime) CurrentUser(c echo.Context) (*User, error) {
	return r.handlers.CurrentUser(c)
}

// LoginPostHandler forwards the legacy login form handler.
func (r *Runtime) LoginPostHandler(c echo.Context) error {
	return r.handlers.LoginPostHandler(c)
}

// SignupPostHandler forwards the legacy signup form handler.
func (r *Runtime) SignupPostHandler(c echo.Context) error {
	return r.handlers.SignupPostHandler(c)
}

// LogoutPostHandler forwards the legacy logout form handler.
func (r *Runtime) LogoutPostHandler(c echo.Context) error {
	return r.handlers.LogoutPostHandler(c)
}

// SignupJSONHandler forwards the SPA signup handler.
func (r *Runtime) SignupJSONHandler(c echo.Context) error {
	return r.handlers.SignupJSONHandler(c)
}

// LoginJSONHandler forwards the SPA login handler.
func (r *Runtime) LoginJSONHandler(c echo.Context) error {
	return r.handlers.LoginJSONHandler(c)
}

// LogoutJSONHandler forwards the SPA logout handler.
func (r *Runtime) LogoutJSONHandler(c echo.Context) error {
	return r.handlers.LogoutJSONHandler(c)
}
