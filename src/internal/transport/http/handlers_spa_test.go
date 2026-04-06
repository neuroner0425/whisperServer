package httptransport

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestSPAIndexHandlerServesBuiltIndex(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html>spa</html>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	h := SPAHandlers{SPAIndexPath: indexPath}
	handler := h.SPAIndexHandler()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
	if body := rec.Body.String(); body != "<html>spa</html>" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestSPAIndexHandlerReturnsUnavailableWhenBuildMissing(t *testing.T) {
	h := SPAHandlers{SPAIndexPath: filepath.Join(t.TempDir(), "missing.html")}
	handler := h.SPAIndexHandler()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := handler(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}
