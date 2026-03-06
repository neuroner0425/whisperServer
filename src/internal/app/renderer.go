package app

import (
	"fmt"
	htmpl "html/template"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
)

func (r *renderer) Render(w io.Writer, name string, data any, _ echo.Context) error {
	t, ok := r.templates[name]
	if !ok {
		return fmt.Errorf("template not found: %s", name)
	}
	return t.Execute(w, data)
}

func mustRenderer() *renderer {
	files := []string{"upload.html", "jobs.html", "waiting.html", "result.html", "preview.html", "login.html", "signup.html"}
	m := make(map[string]*htmpl.Template, len(files))
	for _, name := range files {
		path := filepath.Join(templateDir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("failed to read template %s: %v", name, err)
		}
		t, err := htmpl.New(name).Parse(string(b))
		if err != nil {
			log.Fatalf("failed to parse template %s: %v", name, err)
		}
		m[name] = t
	}
	return &renderer{templates: m}
}
