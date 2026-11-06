package util

import (
	"testing"
)

func TestRenderPDFToJPEGsRange_MissingTool(t *testing.T) {
	_, err := RenderPDFToJPEGsRange("non_existent_pdftoppm", "dummy.pdf", t.TempDir(), 150, 1, 5)
	if err == nil {
		t.Fatalf("expected error for non-existent tool")
	}
}
