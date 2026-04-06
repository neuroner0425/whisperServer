// renderer.go builds the Echo HTML renderer used by the remaining legacy form pages.
package server

import (
	"fmt"
	htmpl "html/template"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
)

type Renderer struct {
	templates map[string]*htmpl.Template
}

// Render resolves a named template and applies the base layout when needed.
func (r *Renderer) Render(w io.Writer, name string, data any, _ echo.Context) error {
	t, ok := r.templates[name]
	if !ok {
		return fmt.Errorf("template not found: %s", name)
	}
	if usesBaseLayout(name) {
		return t.ExecuteTemplate(w, "base", data)
	}
	return t.Execute(w, data)
}

// MustRenderer loads all legacy templates at startup and exits on configuration errors.
func MustRenderer(templateDir string) *Renderer {
	files := []string{"files_upload.html", "files_index.html", "job_waiting.html", "job_result.html", "job_preview.html", "auth_login.html", "auth_signup.html", "tags_index.html", "files_trash.html"}
	m := make(map[string]*htmpl.Template, len(files))
	for _, name := range files {
		layoutPath := filepath.Join(templateDir, "layouts", "base.html")
		pagePath := filepath.Join(templateDir, "pages", name)
		paths := []string{pagePath}
		if usesBaseLayout(name) {
			paths = []string{layoutPath, pagePath}
		}
		var contents []string
		for _, path := range paths {
			b, err := os.ReadFile(path)
			if err != nil {
				log.Fatalf("failed to read template %s: %v", path, err)
			}
			contents = append(contents, string(b))
		}
		t, err := htmpl.New(name).Parse(strings.Join(contents, "\n"))
		if err != nil {
			log.Fatalf("failed to parse template %s: %v", name, err)
		}
		m[name] = t
	}
	return &Renderer{templates: m}
}

// usesBaseLayout decides which legacy pages are still rendered inside the shared layout.
func usesBaseLayout(name string) bool {
	switch name {
	case "auth_login.html", "auth_signup.html", "job_waiting.html", "job_result.html":
		return true
	default:
		return false
	}
}
