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

var pdfPagesRe = regexp.MustCompile(`Pages:\s+(\d+)`)

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

func RenderPDFToJPEGs(toolPath, pdfPath, outDir string, dpi int) ([]string, error) {
	prefix := filepath.Join(outDir, "page")
	cmd := exec.Command(toolPath, "-jpeg", "-r", strconv.Itoa(dpi), pdfPath, prefix)
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

func wrapPDFToolErr(tool string, err error, output []byte) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s is not installed", tool)
	}
	return fmt.Errorf("%s failed: %w | output=%s", tool, err, strings.TrimSpace(string(output)))
}
