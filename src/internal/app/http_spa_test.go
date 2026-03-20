package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestSpaIndexHandlerServesBuiltIndex(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html>spa</html>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	prev := spaIndexPath
	spaIndexPath = indexPath
	t.Cleanup(func() { spaIndexPath = prev })

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := spaIndexHandler(c); err != nil {
		t.Fatalf("spaIndexHandler error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
	if body := rec.Body.String(); body != "<html>spa</html>" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestSpaIndexHandlerReturnsUnavailableWhenBuildMissing(t *testing.T) {
	prev := spaIndexPath
	spaIndexPath = filepath.Join(t.TempDir(), "missing.html")
	t.Cleanup(func() { spaIndexPath = prev })

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := spaIndexHandler(c); err != nil {
		t.Fatalf("spaIndexHandler error: %v", err)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}
