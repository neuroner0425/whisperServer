package httptransport

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/integrations/storage"
)

// LocalBlobHandlers serves and receives direct blob streams for local storage mode.
type LocalBlobHandlers struct {
	Storage storage.Storage
	Logf    func(string, ...any)
	Errf    func(string, error, string, ...any)
}

// Get handles direct presigned GET download and Range streaming.
func (h LocalBlobHandlers) Get() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.Storage == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Storage is not configured")
		}

		key := c.QueryParam("key")
		filename := c.QueryParam("filename")
		expiresStr := c.QueryParam("expires")
		sig := c.QueryParam("sig")

		if strings.TrimSpace(key) == "" || strings.TrimSpace(expiresStr) == "" || strings.TrimSpace(sig) == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "Missing required signing query parameters")
		}

		expiresUnix, err := strconv.ParseInt(expiresStr, 10, 64)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid expiration timestamp")
		}

		if expiresUnix < time.Now().Unix() {
			return echo.NewHTTPError(http.StatusForbidden, "Presigned URL has expired")
		}

		if strings.Contains(key, "..") {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid key: directory traversal detected")
		}

		// Enforce LocalStorage backend and verify HMAC token signature
		ls, ok := h.Storage.(*storage.LocalStorage)
		if !ok || ls == nil {
			return echo.NewHTTPError(http.StatusNotFound, "Local blob endpoint unavailable on current storage backend")
		}
		if !ls.VerifyLocalToken("get", key, filename, expiresUnix, sig) {
			return echo.NewHTTPError(http.StatusForbidden, "Invalid token signature")
		}

		rsc, info, err := h.Storage.GetReadSeeker(c.Request().Context(), key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "Object not found")
			}
			return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to read object: %v", err))
		}
		defer rsc.Close()

		if filename != "" {
			c.Response().Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, url.PathEscape(filename)))
		}
		if info.ContentType != "" {
			c.Response().Header().Set("Content-Type", info.ContentType)
		}

		http.ServeContent(c.Response(), c.Request(), filename, info.LastModified, rsc)
		return nil
	}
}

// Put handles direct presigned PUT streaming uploads.
func (h LocalBlobHandlers) Put() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.Storage == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Storage is not configured")
		}

		key := c.QueryParam("key")
		expiresStr := c.QueryParam("expires")
		sig := c.QueryParam("sig")

		if strings.TrimSpace(key) == "" || strings.TrimSpace(expiresStr) == "" || strings.TrimSpace(sig) == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "Missing required signing query parameters")
		}

		expiresUnix, err := strconv.ParseInt(expiresStr, 10, 64)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid expiration timestamp")
		}

		if expiresUnix < time.Now().Unix() {
			return echo.NewHTTPError(http.StatusForbidden, "Presigned URL has expired")
		}

		if strings.Contains(key, "..") {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid key: directory traversal detected")
		}

		// Enforce LocalStorage backend and verify HMAC token signature
		ls, ok := h.Storage.(*storage.LocalStorage)
		if !ok || ls == nil {
			return echo.NewHTTPError(http.StatusNotFound, "Local blob endpoint unavailable on current storage backend")
		}
		if !ls.VerifyLocalToken("put", key, "", expiresUnix, sig) {
			return echo.NewHTTPError(http.StatusForbidden, "Invalid token signature")
		}

		ct := c.Request().Header.Get("Content-Type")
		size := c.Request().ContentLength

		_, err = h.Storage.PutStream(c.Request().Context(), key, c.Request().Body, size, ct)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("Failed to save stream: %v", err))
		}

		return c.NoContent(http.StatusOK)
	}
}

