package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"

	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

const (
	authCookieName = "ws_auth"
	ctxUserKey     = "auth_user"
)

type User struct {
	ID      string
	LoginID string
	Email   string
}

type Claims struct {
	UserID  string `json:"uid"`
	LoginID string `json:"login_id,omitempty"`
	Email   string `json:"email"`
	jwt.RegisteredClaims
}

type Auth struct {
	jwtSecret        []byte
	jwtIssuer        string
	jwtExpiryHours   int
	authCookieSecure bool
	logf             func(string, ...any)
	errf             func(string, error, string, ...any)
}

func NewAuth(jwtSecret []byte, jwtIssuer string, jwtExpiryHours int, authCookieSecure bool, logf func(string, ...any), errf func(string, error, string, ...any)) *Auth {
	return &Auth{
		jwtSecret:        jwtSecret,
		jwtIssuer:        jwtIssuer,
		jwtExpiryHours:   jwtExpiryHours,
		authCookieSecure: authCookieSecure,
		logf:             logf,
		errf:             errf,
	}
}

func (a *Auth) Middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		p := c.Path()
		if strings.HasPrefix(c.Request().URL.Path, "/static/") ||
			p == routes.Login ||
			p == routes.Signup ||
			p == "/healthz" ||
			strings.HasPrefix(c.Request().URL.Path, "/auth/login") ||
			strings.HasPrefix(c.Request().URL.Path, "/auth/join") ||
			strings.HasPrefix(c.Request().URL.Path, "/api/auth/") {
			return next(c)
		}
		if c.Request().Method == http.MethodPost && (p == routes.Login || p == routes.Signup) {
			return next(c)
		}

		u, err := a.CurrentUserFromRequest(c)
		if err != nil {
			if wantsJSON(c) {
				return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
			}
			return c.Redirect(http.StatusSeeOther, "/auth/login")
		}
		c.Set(ctxUserKey, u)
		return next(c)
	}
}

func wantsJSON(c echo.Context) bool {
	accept := strings.ToLower(c.Request().Header.Get("Accept"))
	if strings.Contains(accept, "application/json") {
		return true
	}
	return strings.HasPrefix(c.Path(), "/status/") || strings.HasPrefix(c.Path(), "/api/")
}

func (a *Auth) CurrentUser(c echo.Context) (*User, error) {
	if v := c.Get(ctxUserKey); v != nil {
		if u, ok := v.(*User); ok {
			return u, nil
		}
	}
	u, err := a.CurrentUserFromRequest(c)
	if err != nil {
		return nil, err
	}
	c.Set(ctxUserKey, u)
	return u, nil
}

func (a *Auth) CurrentUserFromRequest(c echo.Context) (*User, error) {
	cookie, err := c.Cookie(authCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil, errors.New("missing auth cookie")
	}

	token, err := jwt.ParseWithClaims(cookie.Value, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return a.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, errors.New("invalid claims")
	}
	if claims.UserID == "" || claims.Email == "" {
		return nil, errors.New("invalid subject")
	}
	return &User{ID: claims.UserID, LoginID: claims.LoginID, Email: claims.Email}, nil
}

func (a *Auth) issueAuthToken(userID, loginID, email string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:  userID,
		LoginID: loginID,
		Email:   email,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    a.jwtIssuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(a.jwtExpiryHours) * time.Hour)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(a.jwtSecret)
}

func (a *Auth) setAuthCookie(c echo.Context, token string) {
	c.SetCookie(&http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   a.authCookieSecure,
		Expires:  time.Now().Add(time.Duration(a.jwtExpiryHours) * time.Hour),
	})
}

func (a *Auth) clearAuthCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   a.authCookieSecure,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeLoginID(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func validateLoginID(s string) error {
	if len(s) < 3 {
		return errors.New("아이디는 3자 이상이어야 합니다.")
	}
	return nil
}

func validatePassword(pw string) error {
	if len(pw) < 8 {
		return errors.New("비밀번호는 8자 이상이어야 합니다.")
	}
	return nil
}

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func verifyPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func (a *Auth) LoginGetHandler(c echo.Context) error {
	return c.Render(http.StatusOK, "auth_login.html", map[string]any{"Error": ""})
}

func (a *Auth) SignupGetHandler(c echo.Context) error {
	return c.Render(http.StatusOK, "auth_signup.html", map[string]any{"Error": ""})
}

func (a *Auth) SignupPostHandler(c echo.Context) error {
	loginID := normalizeLoginID(c.FormValue("login_id"))
	email := normalizeEmail(c.FormValue("email"))
	pw := c.FormValue("password")
	if loginID == "" || email == "" || pw == "" {
		return c.Render(http.StatusBadRequest, "auth_signup.html", map[string]any{"Error": "아이디, 이메일, 비밀번호를 입력하세요."})
	}
	if err := validateLoginID(loginID); err != nil {
		return c.Render(http.StatusBadRequest, "auth_signup.html", map[string]any{"Error": err.Error()})
	}
	if err := validatePassword(pw); err != nil {
		return c.Render(http.StatusBadRequest, "auth_signup.html", map[string]any{"Error": err.Error()})
	}

	hash, err := hashPassword(pw)
	if err != nil {
		a.errf("auth.signup.hash", err, "email=%s", email)
		return c.Render(http.StatusInternalServerError, "auth_signup.html", map[string]any{"Error": "회원가입 처리 중 오류가 발생했습니다."})
	}
	if err := store.CreateUser(loginID, email, hash); err != nil {
		return c.Render(http.StatusBadRequest, "auth_signup.html", map[string]any{"Error": "이미 존재하는 아이디 또는 이메일입니다."})
	}
	return c.Redirect(http.StatusSeeOther, routes.Login)
}

func (a *Auth) LoginPostHandler(c echo.Context) error {
	identifier := normalizeLoginID(c.FormValue("identifier"))
	pw := c.FormValue("password")
	if identifier == "" || pw == "" {
		return c.Render(http.StatusBadRequest, "auth_login.html", map[string]any{"Error": "아이디(또는 이메일)와 비밀번호를 입력하세요."})
	}

	u, err := store.FindUserByIdentifier(identifier)
	if err != nil || !verifyPassword(u.PasswordHash, pw) {
		return c.Render(http.StatusUnauthorized, "auth_login.html", map[string]any{"Error": "아이디/이메일 또는 비밀번호가 올바르지 않습니다."})
	}
	tok, err := a.issueAuthToken(u.ID, u.LoginID, u.Email)
	if err != nil {
		a.errf("auth.login.issueToken", err, "identifier=%s", identifier)
		return c.Render(http.StatusInternalServerError, "auth_login.html", map[string]any{"Error": "로그인 처리 중 오류가 발생했습니다."})
	}
	a.setAuthCookie(c, tok)
	return c.Redirect(http.StatusSeeOther, routes.Root)
}

func (a *Auth) LogoutPostHandler(c echo.Context) error {
	a.clearAuthCookie(c)
	return c.Redirect(http.StatusSeeOther, routes.Login)
}

func (a *Auth) SignupJSONHandler(c echo.Context) error {
	var body struct {
		LoginID  string `json:"login_id"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"detail": "잘못된 요청입니다."})
	}
	loginID := normalizeLoginID(body.LoginID)
	email := normalizeEmail(body.Email)
	pw := body.Password
	if loginID == "" || email == "" || pw == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"detail": "아이디, 이메일, 비밀번호를 입력하세요."})
	}
	if err := validateLoginID(loginID); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"detail": err.Error()})
	}
	if err := validatePassword(pw); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"detail": err.Error()})
	}
	hash, err := hashPassword(pw)
	if err != nil {
		a.errf("auth.signup.hash", err, "email=%s", email)
		return c.JSON(http.StatusInternalServerError, map[string]string{"detail": "회원가입 처리 중 오류가 발생했습니다."})
	}
	if err := store.CreateUser(loginID, email, hash); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"detail": "이미 존재하는 아이디 또는 이메일입니다."})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (a *Auth) LoginJSONHandler(c echo.Context) error {
	var body struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"detail": "잘못된 요청입니다."})
	}
	identifier := normalizeLoginID(body.Identifier)
	pw := body.Password
	if identifier == "" || pw == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"detail": "아이디(또는 이메일)와 비밀번호를 입력하세요."})
	}
	u, err := store.FindUserByIdentifier(identifier)
	if err != nil || !verifyPassword(u.PasswordHash, pw) {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "아이디/이메일 또는 비밀번호가 올바르지 않습니다."})
	}
	tok, err := a.issueAuthToken(u.ID, u.LoginID, u.Email)
	if err != nil {
		a.errf("auth.login.issueToken", err, "identifier=%s", identifier)
		return c.JSON(http.StatusInternalServerError, map[string]string{"detail": "로그인 처리 중 오류가 발생했습니다."})
	}
	a.setAuthCookie(c, tok)
	return c.JSON(http.StatusOK, map[string]any{
		"status": "ok",
		"user": map[string]string{
			"id":       u.ID,
			"login_id": u.LoginID,
			"email":    u.Email,
		},
	})
}

func (a *Auth) LogoutJSONHandler(c echo.Context) error {
	a.clearAuthCookie(c)
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
