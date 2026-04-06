package httptransport

import (
	"net/url"
	"strings"
)

// SafeReturnPath ensures we only redirect to a same-origin path.
// It mirrors the legacy behavior and falls back to /files/home.
func SafeReturnPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return filesHomePath
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return filesHomePath
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return filesHomePath
	}
	if u.Path == "" {
		u.Path = filesHomePath
	}
	return u.RequestURI()
}
