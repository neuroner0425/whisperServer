package util

import (
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrUploadTooLarge = errors.New("upload too large")

func DetectFileType(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	switch ext {
	case "mp3", "wav", "m4a":
		return "audio"
	case "pdf":
		return "pdf"
	case "ppt", "pptx":
		return "ppt"
	default:
		return "unknown"
	}
}

func AllowedFile(name string, allowedExtensions map[string]struct{}) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	_, ok := allowedExtensions[ext]
	return ok
}

func SecureFilename(name string, secureRe *regexp.Regexp) string {
	base := filepath.Base(name)
	base = strings.ReplaceAll(base, " ", "_")
	base = secureRe.ReplaceAllString(base, "_")
	if base == "" || base == "." || base == ".." {
		return "file"
	}
	return base
}

func SaveUploadWithLimit(h *multipart.FileHeader, dst string, maxBytes int64, chunkSize int, bytesPerSec int64) (int64, error) {
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
	startedAt := time.Now()
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxBytes {
				return written, ErrUploadTooLarge
			}
			if _, err := out.Write(buf[:n]); err != nil {
				return written, err
			}
			if bytesPerSec > 0 {
				expectedElapsed := time.Duration(float64(written) / float64(bytesPerSec) * float64(time.Second))
				if sleepFor := time.Until(startedAt.Add(expectedElapsed)); sleepFor > 0 {
					time.Sleep(sleepFor)
				}
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

func ConvertToWav(src, dst string) error {
	cmd := exec.Command(
		"ffmpeg",
		"-y",
		"-i", src,
		"-ac", "1",
		"-ar", "16000",
		dst,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %s", string(out))
	}
	return nil
}

func ConvertToAac(src, dst string) error {
	cmd := exec.Command(
		"ffmpeg",
		"-y",
		"-i", src,
		"-vn",
		"-c:a", "aac",
		"-b:a", "192k",
		"-ar", "48000",
		dst,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %s", string(out))
	}
	return nil
}

func GetMediaDuration(path string) *int {
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

func FormatSecondsPtr(sec *int) string {
	if sec == nil {
		return "-"
	}
	return FormatSeconds(*sec)
}

func FormatSeconds(sec int) string {
	h := sec / 3600
	r := sec % 3600
	m := r / 60
	s := r % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func SortedExts(allowedExtensions map[string]struct{}) []string {
	out := make([]string, 0, len(allowedExtensions))
	for k := range allowedExtensions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
