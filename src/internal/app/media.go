package app

import (
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var errUploadTooLarge = errors.New("upload too large")

func allowedFile(name string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	_, ok := allowedExtensions[ext]
	return ok
}

func secureFilename(name string) string {
	base := filepath.Base(name)
	base = strings.ReplaceAll(base, " ", "_")
	base = secureRe.ReplaceAllString(base, "_")
	if base == "" || base == "." || base == ".." {
		return "file"
	}
	return base
}

func saveUploadWithLimit(h *multipart.FileHeader, dst string, maxBytes int64) (int64, error) {
	src, err := h.Open()
	if err != nil {
		return 0, err
	}
	defer src.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	buf := make([]byte, chunkSize)
	var written int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxBytes {
				return written, errUploadTooLarge
			}
			if _, err := out.Write(buf[:n]); err != nil {
				return written, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return written, readErr
		}
	}
	return written, nil
}

func convertToWav(src, dst string) error {
	cmd := exec.Command("ffmpeg", "-y", "-i", src, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %s", string(out))
	}
	return nil
}

func getMediaDuration(path string) *int {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(strings.Split(s, "\n")[0], 64)
	if err != nil {
		return nil
	}
	v := int(math.Round(f))
	return &v
}

func formatSecondsPtr(sec *int) string {
	if sec == nil {
		return "-"
	}
	return formatSeconds(*sec)
}

func formatSeconds(sec int) string {
	h := sec / 3600
	r := sec % 3600
	m := r / 60
	s := r % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func sortedExts() []string {
	out := make([]string, 0, len(allowedExtensions))
	for k := range allowedExtensions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
