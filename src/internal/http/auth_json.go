package httpx

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/store"
)

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
