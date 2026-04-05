package httptransport

import (
	"net/url"
	"strings"

	"whisperserver/src/internal/routes"
)

// SafeReturnPath ensures we only redirect to a same-origin path.
// It mirrors the legacy behavior and falls back to /files/home.
func SafeReturnPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return routes.FilesHome
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return routes.FilesHome
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return routes.FilesHome
	}
	if u.Path == "" {
		u.Path = routes.FilesHome
	}
	return u.RequestURI()
}

