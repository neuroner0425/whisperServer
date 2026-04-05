package httpx

import (
	htmpl "html/template"

	"github.com/labstack/echo/v4"
	"whisperserver/src/internal/model"
)

type JobsDeps struct {
	CurrentUser         func(echo.Context) (*User, error)
	CurrentUserName     func(echo.Context) string
	RequireOwnedJob     func(echo.Context, string, bool) (*model.Job, *User, error)
	DisableCache        func(echo.Context)
	NormalizeSortParams func(string, string) (string, string)
	NormalizeFolderID   func(string) string
	ParsePositiveInt    func(string, int) int
	PaginateRows        func([]JobRow, int, int) ([]JobRow, int, int)
	BuildRecentJobRows  func(string, string, string) []JobRow
	BuildJobRows        func(string, string, string, string, bool) []JobRow
	BuildFolderRows     func(string, string, string) []FolderRow
	RecentFolderRows    func(string) []FolderRow
	SortFolderRows      func([]FolderRow, string, string)
	SortJobRows         func([]JobRow, string, string)
	JobsSnapshotVersion func([]JobRow, []FolderRow, int, int, int, int) string
	SelectedTagMap      func([]string) map[string]bool
	ToJobView           func(*model.Job) JobView
	RenderResultText    func(string, bool, *int) htmpl.HTML
	RenderMarkdownText  func(string) htmpl.HTML
	Fallback            func(string, string) string
	SanitizePreviewText func(string) string
	HasGeminiConfigured func() bool
	SetJobFields        func(string, map[string]any)
	EnqueueRefine       func(string)
	GetJob              func(string) *model.Job
	IsJobTrashed        func(*model.Job) bool
	Logf                func(string, ...any)
	Errf                func(string, error, string, ...any)
}
