package httpx

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/routes"
	"whisperserver/src/internal/store"
)

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
