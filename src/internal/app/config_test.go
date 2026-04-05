package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePDFConfigValues(t *testing.T) {
	origMaxPages := pdfMaxPages
	origMaxPagesPerRequest := pdfMaxPagesPerRequest
	origRenderDPI := pdfRenderDPI
	origBatchTimeoutSec := pdfBatchTimeoutSec
	origMaxRenderedImageBytes := pdfMaxRenderedImageBytes
	origContextChars := pdfConsistencyContextMaxChars
	origToolInfo := pdfToolPDFInfo
	origToolPPM := pdfToolPDFToPPM
	t.Cleanup(func() {
		pdfMaxPages = origMaxPages
		pdfMaxPagesPerRequest = origMaxPagesPerRequest
		pdfRenderDPI = origRenderDPI
		pdfBatchTimeoutSec = origBatchTimeoutSec
		pdfMaxRenderedImageBytes = origMaxRenderedImageBytes
		pdfConsistencyContextMaxChars = origContextChars
		pdfToolPDFInfo = origToolInfo
		pdfToolPDFToPPM = origToolPPM
	})

	pdfMaxPages = 300
	pdfMaxPagesPerRequest = 80
	pdfRenderDPI = 144
	pdfBatchTimeoutSec = 180
	pdfMaxRenderedImageBytes = 1024
	pdfConsistencyContextMaxChars = 100
	pdfToolPDFInfo = "pdfinfo"
	pdfToolPDFToPPM = "pdftoppm"

	if err := validatePDFConfigValues(); err != nil {
		t.Fatalf("validatePDFConfigValues returned error: %v", err)
	}

	pdfMaxPagesPerRequest = 400
	if err := validatePDFConfigValues(); err == nil {
		t.Fatalf("expected validation error when chunk size exceeds max pages")
	}
}

func TestValidatePDFTools(t *testing.T) {
	dir := t.TempDir()
	infoPath := filepath.Join(dir, "pdfinfo")
	ppmPath := filepath.Join(dir, "pdftoppm")
	if err := os.WriteFile(infoPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write info tool: %v", err)
	}
	if err := os.WriteFile(ppmPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write ppm tool: %v", err)
	}

	origToolInfo := pdfToolPDFInfo
	origToolPPM := pdfToolPDFToPPM
	t.Cleanup(func() {
		pdfToolPDFInfo = origToolInfo
		pdfToolPDFToPPM = origToolPPM
	})

	pdfToolPDFInfo = infoPath
	pdfToolPDFToPPM = ppmPath
	if err := validatePDFTools(); err != nil {
		t.Fatalf("validatePDFTools returned error: %v", err)
	}

	pdfToolPDFToPPM = filepath.Join(dir, "missing-pdftoppm")
	if err := validatePDFTools(); err == nil {
		t.Fatalf("expected tool validation error")
	}
}
