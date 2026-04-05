package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestAuthCurrentUserFromRequestValidToken(t *testing.T) {
	a := NewAuth([]byte("test-secret-1234567890"), "test-issuer", 24, false, nil, nil)
	token, err := a.issueAuthToken("user-1", "login1", "user@example.com")
	if err != nil {
		t.Fatalf("issueAuthToken() error = %v", err)
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	u, err := a.CurrentUserFromRequest(c)
	if err != nil {
		t.Fatalf("CurrentUserFromRequest() error = %v", err)
	}
	if u == nil {
		t.Fatal("CurrentUserFromRequest() returned nil user")
	}
	if u.ID != "user-1" || u.LoginID != "login1" || u.Email != "user@example.com" {
		t.Fatalf("unexpected user: %#v", u)
	}
}

func TestAuthMiddlewareUnauthorizedAPIReturnsJSON(t *testing.T) {
	a := NewAuth([]byte("test-secret-1234567890"), "test-issuer", 24, false, nil, nil)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/me")

	nextCalled := false
	err := a.Middleware(func(c echo.Context) error {
		nextCalled = true
		return c.NoContent(http.StatusOK)
	})(c)
	if err != nil {
		t.Fatalf("middleware returned error: %v", err)
	}
	if nextCalled {
		t.Fatal("next handler should not be called for unauthorized API request")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddlewareUnauthorizedPageRedirects(t *testing.T) {
	a := NewAuth([]byte("test-secret-1234567890"), "test-issuer", 24, false, nil, nil)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/files/home", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/files/home")

	nextCalled := false
	err := a.Middleware(func(c echo.Context) error {
		nextCalled = true
		return c.NoContent(http.StatusOK)
	})(c)
	if err != nil {
		t.Fatalf("middleware returned error: %v", err)
	}
	if nextCalled {
		t.Fatal("next handler should not be called for unauthorized page request")
	}
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/auth/login" {
		t.Fatalf("redirect location = %q, want %q", got, "/auth/login")
	}
}

func TestRuntimeNewWithEphemeralSecretCanValidateIssuedToken(t *testing.T) {
	r := New(Config{
		JWTSecret:        nil,
		JWTIssuer:        "test-issuer",
		JWTExpiryHours:   24,
		AuthCookieSecure: false,
	}, nil, nil)
	if r == nil || r.handlers == nil {
		t.Fatal("runtime or handlers should not be nil")
	}

	token, err := r.handlers.issueAuthToken("user-2", "login2", "two@example.com")
	if err != nil {
		t.Fatalf("issueAuthToken() error = %v", err)
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	u, err := r.CurrentUser(c)
	if err != nil {
		t.Fatalf("CurrentUser() error = %v", err)
	}
	if u == nil || u.ID != "user-2" {
		t.Fatalf("unexpected user: %#v", u)
	}
}
