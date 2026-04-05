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

func (r *Runtime) Middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return r.handlers.Middleware(next)
}

func (r *Runtime) CurrentUser(c echo.Context) (*User, error) {
	return r.handlers.CurrentUser(c)
}

func (r *Runtime) LoginPostHandler(c echo.Context) error {
	return r.handlers.LoginPostHandler(c)
}

func (r *Runtime) SignupPostHandler(c echo.Context) error {
	return r.handlers.SignupPostHandler(c)
}

func (r *Runtime) LogoutPostHandler(c echo.Context) error {
	return r.handlers.LogoutPostHandler(c)
}

func (r *Runtime) SignupJSONHandler(c echo.Context) error {
	return r.handlers.SignupJSONHandler(c)
}

func (r *Runtime) LoginJSONHandler(c echo.Context) error {
	return r.handlers.LoginJSONHandler(c)
}

func (r *Runtime) LogoutJSONHandler(c echo.Context) error {
	return r.handlers.LogoutJSONHandler(c)
}
