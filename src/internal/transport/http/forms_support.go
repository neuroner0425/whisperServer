package httptransport

import (
	"strings"

	"github.com/labstack/echo/v4"
)

// NormalizeFolderID trims folder identifiers received from forms or query strings.
func NormalizeFolderID(v string) string {
	return strings.TrimSpace(v)
}

// ParseSelectedTags reads repeated `tags` fields from multipart or urlencoded forms.
func ParseSelectedTags(c echo.Context, uniqueStrings func([]string) []string) []string {
	r := c.Request()
	if err := r.ParseMultipartForm(32 << 20); err == nil && r.MultipartForm != nil {
		return uniqueStrings(r.MultipartForm.Value["tags"])
	}
	if err := r.ParseForm(); err == nil {
		return uniqueStrings(r.Form["tags"])
	}
	return nil
}
