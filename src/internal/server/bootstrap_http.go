// bootstrap_http.go wires Echo-level helpers that are shared across handlers.
package server

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	model "whisperserver/src/internal/domain"
	httptransport "whisperserver/src/internal/transport/http"
	intutil "whisperserver/src/internal/util"
)

// newHTTPServer creates the Echo instance with shared middleware, renderer, and auth.
func newHTTPServer() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Skipper: func(c echo.Context) bool {
			p := c.Path()
			return strings.HasPrefix(p, "/status/") || p == "/jobs/updates"
		},
	}))
	e.Renderer = MustRenderer(templateDir)
	e.Use(authRuntime.Middleware)
	return e
}

// transportCurrentUser adapts auth.User into the transport DTO used by handlers.
func transportCurrentUser(c echo.Context) (*httptransport.User, bool) {
	u, err := currentUser(c)
	if err != nil || u == nil {
		return nil, false
	}
	return &httptransport.User{ID: u.ID, LoginID: u.LoginID, Email: u.Email}, true
}

// currentUserName returns the display name preferred by the current UI.
func currentUserName(c echo.Context) string {
	u, err := currentUser(c)
	if err != nil || u == nil {
		return ""
	}
	if u.LoginID != "" {
		return u.LoginID
	}
	return u.Email
}

// currentUserOrUnauthorized centralizes the JSON 401 response used by API handlers.
func currentUserOrUnauthorized(c echo.Context) (*AuthUser, error) {
	u, err := currentUser(c)
	if err == nil {
		return u, nil
	}
	_ = c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
	return nil, err
}

// transportCurrentUserOrUnauthorized is the transport-facing version of currentUserOrUnauthorized.
func transportCurrentUserOrUnauthorized(c echo.Context) (*httptransport.User, bool) {
	u, err := currentUserOrUnauthorized(c)
	if err != nil || u == nil {
		return nil, false
	}
	return &httptransport.User{ID: u.ID, LoginID: u.LoginID, Email: u.Email}, true
}

// toAPIJobView maps the runtime job model into the job view returned by the JSON detail API.
func toAPIJobView(job *model.Job) JobView {
	return JobView{
		Filename:           job.Filename,
		FileType:           job.FileType,
		Status:             job.Status,
		UploadedAt:         intutil.Fallback(job.UploadedAt, "-"),
		StartedAt:          intutil.Fallback(job.StartedAt, "-"),
		CompletedAt:        intutil.Fallback(job.CompletedAt, "-"),
		Duration:           jobViewDuration(job.Duration),
		MediaDuration:      intutil.Fallback(job.MediaDuration, "-"),
		Phase:              intutil.Fallback(job.Phase, "대기 중"),
		ProgressLabel:      intutil.Fallback(job.ProgressLabel, ""),
		ProgressPercent:    job.ProgressPercent,
		PreviewText:        job.PreviewText,
		StatusDetail:       job.StatusDetail,
		PageCount:          job.PageCount,
		ProcessedPageCount: job.ProcessedPageCount,
		CurrentChunk:       job.CurrentChunk,
		TotalChunks:        job.TotalChunks,
		ResumeAvailable:    job.ResumeAvailable,
	}
}

// jobViewDuration normalizes optional duration values into the UI placeholder format.
func jobViewDuration(v any) string {
	if v == nil {
		return "-"
	}
	s := intutil.AsString(v)
	if strings.TrimSpace(s) == "" || s == "<nil>" {
		return "-"
	}
	return s
}

// registerHTTPRoutes attaches the transport route table to the Echo server.
func registerHTTPRoutes(e *echo.Echo, svc appServices) {
	spaH := newSPAHandlers()
	spaIndex := spaH.SPAIndexHandler()
	httptransport.RegisterRoutes(e, httptransport.Config{StaticDir: staticDir}, buildRouteHandlers(svc, spaIndex))
}
