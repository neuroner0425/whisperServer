// paths.go stores bootstrap-scoped filesystem paths and mutable runtime tuning values.
package server

import (
	"os"
	"path/filepath"
	"regexp"
)

var (
	projectRoot, _ = os.Getwd()
	tmpFolder      = filepath.Join(projectRoot, ".run", "tmp")
	templateDir    = filepath.Join(projectRoot, "templates")
	staticDir      = filepath.Join(projectRoot, "static")
	spaIndexPath   = filepath.Join(staticDir, "app", "index.html")
	modelDir       = filepath.Join(projectRoot, "whisper", "models")
	whisperCLI     = filepath.Join(projectRoot, "whisper", "bin", "whisper-cli")

	allowedExtensions             = map[string]struct{}{"mp3": {}, "wav": {}, "m4a": {}, "pdf": {}}
	chunkSize                     = 4 * 1024 * 1024
	maxUploadSizeMB               int
	uploadRateLimitKB             int
	jobTimeoutSec                 int
	runMode                       string
	geminiModel                   string
	splitTaskQueues               bool
	pdfMaxPages                   int
	pdfMaxPagesPerRequest         int
	pdfRenderDPI                  int
	pdfBatchTimeoutSec            int
	pdfMaxRenderedImageBytes      int64
	pdfConsistencyContextMaxChars int
	pdfToolPDFInfo                string
	pdfToolPDFToPPM               string

	storageType       string
	s3Endpoint        string
	s3Region          string
	s3Bucket          string
	s3AccessKeyID     string
	s3SecretAccessKey string
	s3UseSSL          bool

	progressRe = regexp.MustCompile(`\[(\d{2}):(\d{2}):(\d{2}(?:\.\d+)?)\s*-->`)
)

func isDevMode() bool {
	return runMode == "DEV"
}
