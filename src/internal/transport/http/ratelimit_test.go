package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/time/rate"
	"whisperserver/src/internal/auth"
)

func TestRateLimitByIP(t *testing.T) {
	e := echo.New()
	limiter := RateLimitByIP(rate.Every(1*time.Minute), 2) // burst: 2

	handler := limiter(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	runReq := func(ip string) int {
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.RemoteAddr = ip + ":12345"
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		err := handler(c)
		if err != nil {
			if he, ok := err.(*echo.HTTPError); ok {
				return he.Code
			}
			return http.StatusInternalServerError
		}
		return rec.Code
	}

	// First 2 requests from IP A should succeed
	if code := runReq("192.168.1.1"); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if code := runReq("192.168.1.1"); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	// 3rd request from IP A should be rate limited (429)
	if code := runReq("192.168.1.1"); code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", code)
	}

	// But IP B should succeed
	if code := runReq("192.168.1.2"); code != http.StatusOK {
		t.Fatalf("expected 200 for IP B, got %d", code)
	}
}

func TestRateLimitByUserOrIP(t *testing.T) {
	e := echo.New()
	limiter := RateLimitByUserOrIP(rate.Every(1*time.Minute), 1, func(c echo.Context) string {
		if v := c.Get("auth_user"); v != nil {
			if u, ok := v.(*auth.User); ok && u != nil && u.ID != "" {
				return "user:" + u.ID
			}
		}
		return ""
	})

	handler := limiter(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	runUserReq := func(userID string, ip string) int {
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.RemoteAddr = ip + ":12345"
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		if userID != "" {
			c.Set("auth_user", &auth.User{ID: userID})
		}
		err := handler(c)
		if err != nil {
			if he, ok := err.(*echo.HTTPError); ok {
				return he.Code
			}
			return http.StatusInternalServerError
		}
		return rec.Code
	}

	// User 1 first request OK
	if code := runUserReq("user1", "10.0.0.1"); code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	// User 1 second request limited even from different IP
	if code := runUserReq("user1", "10.0.0.2"); code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", code)
	}

	// User 2 from same IP should succeed
	if code := runUserReq("user2", "10.0.0.1"); code != http.StatusOK {
		t.Fatalf("expected 200 for user2, got %d", code)
	}
}

func TestRobotsTxtRoute(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, Config{StaticDir: "/tmp"}, Handlers{})

	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if body != "User-agent: *\nDisallow: /api/\nDisallow: /download/\n" {
		t.Fatalf("unexpected robots.txt content: %q", body)
	}
}

