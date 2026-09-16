package httptransport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
)

// formatContentDisposition creates an RFC 5987 compliant Content-Disposition header value
// that ensures proper filename decoding across Safari, Chrome, Edge, and mobile browsers.
func formatContentDisposition(filename string) string {
	// 1. Build an ASCII fallback name (replacing non-ASCII and quotes with underscore)
	var sb strings.Builder
	for _, r := range filename {
		if r > 127 || r == '"' || r == '\\' || r == '/' {
			sb.WriteByte('_')
		} else {
			sb.WriteRune(r)
		}
	}
	asciiName := sb.String()
	if strings.TrimSpace(asciiName) == "" {
		asciiName = "download"
	}

	// 2. Encode UTF-8 filename according to RFC 5987
	encoded := url.PathEscape(filename)

	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, asciiName, encoded)
}

// setContentDisposition sets the Content-Disposition header with RFC 5987 standard compliance.
func setContentDisposition(c echo.Context, filename string) {
	c.Response().Header().Set(echo.HeaderContentDisposition, formatContentDisposition(filename))
}
