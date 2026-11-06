package httptransport

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/time/rate"
)

type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// IPRateLimiter tracks rate limiters by client key (IP or user ID).
type IPRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*clientLimiter
	rate     rate.Limit
	burst    int
	cancel   context.CancelFunc
}

// NewIPRateLimiter creates a key-based rate limiter with a background garbage collection.
func NewIPRateLimiter(r rate.Limit, burst int) *IPRateLimiter {
	ctx, cancel := context.WithCancel(context.Background())
	lim := &IPRateLimiter{
		limiters: make(map[string]*clientLimiter),
		rate:     r,
		burst:    burst,
		cancel:   cancel,
	}
	go lim.cleanupStale(ctx, 5*time.Minute)
	return lim
}

// Stop terminates the background cleanup goroutine.
func (l *IPRateLimiter) Stop() {
	if l != nil && l.cancel != nil {
		l.cancel()
	}
}

func (l *IPRateLimiter) getLimiter(key string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	item, exists := l.limiters[key]
	if !exists {
		lim := rate.NewLimiter(l.rate, l.burst)
		l.limiters[key] = &clientLimiter{limiter: lim, lastSeen: time.Now()}
		return lim
	}
	item.lastSeen = time.Now()
	return item.limiter
}

func (l *IPRateLimiter) cleanupStale(ctx context.Context, ttl time.Duration) {
	ticker := time.NewTicker(ttl)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.mu.Lock()
			now := time.Now()
			for key, item := range l.limiters {
				if now.Sub(item.lastSeen) > ttl {
					delete(l.limiters, key)
				}
			}
			l.mu.Unlock()
		}
	}
}

// RateLimitByIP returns an Echo middleware that limits requests by client IP.
func RateLimitByIP(r rate.Limit, burst int) echo.MiddlewareFunc {
	limiter := NewIPRateLimiter(r, burst)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ip := c.RealIP()
			if ip == "" {
				ip = c.Request().RemoteAddr
			}
			if !limiter.getLimiter(ip).Allow() {
				c.Response().Header().Set("Retry-After", "60")
				return echo.NewHTTPError(http.StatusTooManyRequests, "요청 한도를 초과했습니다. 잠시 후 다시 시도하세요.")
			}
			return next(c)
		}
	}
}

// RateLimitByUserOrIP returns an Echo middleware that limits requests by authenticated user ID (or fallback to IP).
func RateLimitByUserOrIP(r rate.Limit, burst int, getUserID func(echo.Context) string) echo.MiddlewareFunc {
	limiter := NewIPRateLimiter(r, burst)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key := ""
			if getUserID != nil {
				key = getUserID(c)
			}
			if key == "" {
				key = c.RealIP()
				if key == "" {
					key = c.Request().RemoteAddr
				}
			}
			if !limiter.getLimiter(key).Allow() {
				c.Response().Header().Set("Retry-After", "60")
				return echo.NewHTTPError(http.StatusTooManyRequests, "업로드 요청 한도를 초과했습니다. 잠시 후 다시 시도하세요.")
			}
			return next(c)
		}
	}
}

