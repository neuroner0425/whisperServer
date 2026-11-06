package util

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// pdfPagesRe extracts the page count from `pdfinfo` output.
var pdfPagesRe = regexp.MustCompile(`Pages:\s+(\d+)`)

// CountPDFPages asks `pdfinfo` for the number of pages in a PDF.
func CountPDFPages(toolPath, pdfPath string) (int, error) {
	cmd := exec.Command(toolPath, pdfPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, wrapPDFToolErr("pdfinfo", err, out)
	}
	m := pdfPagesRe.FindStringSubmatch(string(out))
	if len(m) != 2 {
		return 0, fmt.Errorf("pdfinfo output missing page count")
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(m[1]))
	if convErr != nil {
		return 0, convErr
	}
	return n, nil
}

// RenderPDFToJPEGs renders each PDF page into a JPEG file for OCR-style processing.
func RenderPDFToJPEGs(toolPath, pdfPath, outDir string, dpi int) ([]string, error) {
	return RenderPDFToJPEGsRange(toolPath, pdfPath, outDir, dpi, 0, 0)
}

// RenderPDFToJPEGsRange renders only the specified page range [startPage, endPage] into JPEGs (1-based index).
// If startPage <= 0 or endPage <= 0, all pages are rendered.
func RenderPDFToJPEGsRange(toolPath, pdfPath, outDir string, dpi, startPage, endPage int) ([]string, error) {
	prefix := filepath.Join(outDir, "page")
	args := []string{"-jpeg", "-r", strconv.Itoa(dpi)}
	if startPage > 0 && endPage >= startPage {
		args = append(args, "-f", strconv.Itoa(startPage), "-l", strconv.Itoa(endPage))
	}
	args = append(args, pdfPath, prefix)

	cmd := exec.Command(toolPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, wrapPDFToolErr("pdftoppm", err, out)
	}
	matches, err := filepath.Glob(prefix + "-*.jpg")
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

// wrapPDFToolErr normalizes poppler command failures into readable errors.
func wrapPDFToolErr(tool string, err error, output []byte) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s is not installed", tool)
	}
	return fmt.Errorf("%s failed: %w | output=%s", tool, err, strings.TrimSpace(string(output)))
}
