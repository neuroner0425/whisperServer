package app

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

const (
	authCookieName = "ws_auth"
	ctxUserKey     = "auth_user"
)

type AuthUser struct {
	ID      string
	LoginID string
	Email   string
}

type authClaims struct {
	UserID  string `json:"uid"`
	LoginID string `json:"login_id,omitempty"`
	Email   string `json:"email"`
	jwt.RegisteredClaims
}

var (
	jwtSecret        = []byte(envString("JWT_SECRET", ""))
	jwtIssuer        = envString("JWT_ISSUER", "whisperserver")
	jwtExpiryHours   = envInt("JWT_EXP_HOURS", 24)
	authCookieSecure = truthy(envString("AUTH_COOKIE_SECURE", "false"))
)

func authMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		p := c.Path()
		if strings.HasPrefix(c.Request().URL.Path, "/static/") || p == "/login" || p == "/signup" || p == "/healthz" {
			return next(c)
		}
		if c.Request().Method == http.MethodPost && (p == "/login" || p == "/signup") {
			return next(c)
		}

		u, err := currentUserFromRequest(c)
		if err != nil {
			if wantsJSON(c) {
				return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
			}
			return c.Redirect(http.StatusSeeOther, "/login")
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
	if strings.HasPrefix(c.Path(), "/status/") {
		return true
	}
	return false
}

func currentUser(c echo.Context) (*AuthUser, error) {
	if v := c.Get(ctxUserKey); v != nil {
		if u, ok := v.(*AuthUser); ok {
			return u, nil
		}
	}
	u, err := currentUserFromRequest(c)
	if err != nil {
		return nil, err
	}
	c.Set(ctxUserKey, u)
	return u, nil
}

func currentUserFromRequest(c echo.Context) (*AuthUser, error) {
	cookie, err := c.Cookie(authCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil, errors.New("missing auth cookie")
	}

	token, err := jwt.ParseWithClaims(cookie.Value, &authClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}

	claims, ok := token.Claims.(*authClaims)
	if !ok {
		return nil, errors.New("invalid claims")
	}
	if claims.UserID == "" || claims.Email == "" {
		return nil, errors.New("invalid subject")
	}
	return &AuthUser{ID: claims.UserID, LoginID: claims.LoginID, Email: claims.Email}, nil
}

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

func issueAuthToken(userID, loginID, email string) (string, error) {
	now := time.Now()
	claims := authClaims{
		UserID:  userID,
		LoginID: loginID,
		Email:   email,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(jwtExpiryHours) * time.Hour)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(jwtSecret)
}

func setAuthCookie(c echo.Context, token string) {
	cookie := &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   authCookieSecure,
		Expires:  time.Now().Add(time.Duration(jwtExpiryHours) * time.Hour),
	}
	c.SetCookie(cookie)
}

func clearAuthCookie(c echo.Context) {
	cookie := &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   authCookieSecure,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	}
	c.SetCookie(cookie)
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

func loginGetHandler(c echo.Context) error {
	return c.Render(http.StatusOK, "login.html", map[string]any{"Error": ""})
}

func signupGetHandler(c echo.Context) error {
	return c.Render(http.StatusOK, "signup.html", map[string]any{"Error": ""})
}

func signupPostHandler(c echo.Context) error {
	loginID := normalizeLoginID(c.FormValue("login_id"))
	email := normalizeEmail(c.FormValue("email"))
	pw := c.FormValue("password")
	if loginID == "" || email == "" || pw == "" {
		return c.Render(http.StatusBadRequest, "signup.html", map[string]any{"Error": "아이디, 이메일, 비밀번호를 입력하세요."})
	}
	if err := validateLoginID(loginID); err != nil {
		return c.Render(http.StatusBadRequest, "signup.html", map[string]any{"Error": err.Error()})
	}
	if err := validatePassword(pw); err != nil {
		return c.Render(http.StatusBadRequest, "signup.html", map[string]any{"Error": err.Error()})
	}

	hash, err := hashPassword(pw)
	if err != nil {
		procErrf("auth.signup.hash", err, "email=%s", email)
		return c.Render(http.StatusInternalServerError, "signup.html", map[string]any{"Error": "회원가입 처리 중 오류가 발생했습니다."})
	}
	if err := createUser(loginID, email, hash); err != nil {
		return c.Render(http.StatusBadRequest, "signup.html", map[string]any{"Error": "이미 존재하는 아이디 또는 이메일입니다."})
	}
	return c.Redirect(http.StatusSeeOther, "/login")
}

func loginPostHandler(c echo.Context) error {
	identifier := normalizeLoginID(c.FormValue("identifier"))
	pw := c.FormValue("password")
	if identifier == "" || pw == "" {
		return c.Render(http.StatusBadRequest, "login.html", map[string]any{"Error": "아이디(또는 이메일)와 비밀번호를 입력하세요."})
	}

	u, err := findUserByIdentifier(identifier)
	if err != nil || !verifyPassword(u.PasswordHash, pw) {
		return c.Render(http.StatusUnauthorized, "login.html", map[string]any{"Error": "아이디/이메일 또는 비밀번호가 올바르지 않습니다."})
	}
	tok, err := issueAuthToken(u.ID, u.LoginID, u.Email)
	if err != nil {
		procErrf("auth.login.issueToken", err, "identifier=%s", identifier)
		return c.Render(http.StatusInternalServerError, "login.html", map[string]any{"Error": "로그인 처리 중 오류가 발생했습니다."})
	}
	setAuthCookie(c, tok)
	return c.Redirect(http.StatusSeeOther, "/")
}

func logoutPostHandler(c echo.Context) error {
	clearAuthCookie(c)
	return c.Redirect(http.StatusSeeOther, "/login")
}
