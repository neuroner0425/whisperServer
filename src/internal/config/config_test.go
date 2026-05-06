package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRunModeDefaultsToProduction(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, "")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.RunMode != "PRODUCTION" {
		t.Fatalf("RunMode = %q, want PRODUCTION", cfg.RunMode)
	}
}

func TestLoadRunModeDev(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, "RUN_MODE=DEV\n")

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.RunMode != "DEV" {
		t.Fatalf("RunMode = %q, want DEV", cfg.RunMode)
	}
}

func TestLoadRunModeRejectsInvalidValue(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, "RUN_MODE=LOCAL\n")

	if _, err := Load(root); err == nil {
		t.Fatal("Load() should reject invalid RUN_MODE")
	}
}

func writeTestConfig(t *testing.T, root, runModeLine string) {
	t.Helper()
	content := `PORT=8000
` + runModeLine + `MAX_UPLOAD_SIZE_MB=512
UPLOAD_RATE_LIMIT_KBPS=0
JOB_TIMEOUT_SEC=3600
GEMINI_MODEL=gemini-flash-lite-latest
SPLIT_TRANSCRIBE_REFINE_QUEUE=false
PDF_MAX_PAGES=300
PDF_MAX_PAGES_PER_REQUEST=50
PDF_RENDER_DPI=144
PDF_BATCH_TIMEOUT_SEC=300
PDF_MAX_RENDERED_IMAGE_BYTES=209715200
PDF_CONSISTENCY_CONTEXT_MAX_CHARS=6000
PDF_TOOL_PDFINFO=pdfinfo
PDF_TOOL_PDFTOPPM=pdftoppm
JWT_SECRET=
JWT_ISSUER=whisperserver
JWT_EXP_HOURS=24
AUTH_COOKIE_SECURE=false
GEMINI_API_KEYS=[]
`
	if err := os.WriteFile(filepath.Join(root, "app.conf.default"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
