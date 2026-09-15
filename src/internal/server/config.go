package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"whisperserver/src/internal/config"
	"whisperserver/src/internal/integrations/storage"
	store "whisperserver/src/internal/repo/sqlite"
)

var (
	configOnce   sync.Once
	configErr    error
	configPath   string
	loadedConfig config.Config
)

// ensureConfigLoaded initializes config values on first use.
func ensureConfigLoaded() error {
	configOnce.Do(func() {
		loadedConfig, configErr = config.Load(projectRoot)
		if configErr == nil {
			configPath = loadedConfig.SourcePath
		}
	})
	return configErr
}

// initRuntimeConfig loads and validates runtime configuration values.
func initRuntimeConfig() error {
	if err := ensureConfigLoaded(); err != nil {
		return err
	}

	maxUploadSizeMB = loadedConfig.MaxUploadSizeMB
	uploadRateLimitKB = loadedConfig.UploadRateLimitKBPS
	jobTimeoutSec = loadedConfig.JobTimeoutSec
	runMode = loadedConfig.RunMode
	splitTaskQueues = loadedConfig.SplitTaskQueues

	geminiModel = loadedConfig.GeminiModel

	jwtSecret = loadedConfig.JWTSecret
	jwtIssuer = effectiveJWTIssuer(loadedConfig.JWTIssuer, loadedConfig.RunMode)
	jwtExpiryHours = loadedConfig.JWTExpiryHours
	authCookieSecure = loadedConfig.AuthCookieSecure

	pdfMaxPages = loadedConfig.PDFMaxPages
	pdfMaxPagesPerRequest = loadedConfig.PDFMaxPagesPerReq
	pdfRenderDPI = loadedConfig.PDFRenderDPI
	pdfBatchTimeoutSec = loadedConfig.PDFBatchTimeoutSec
	pdfMaxRenderedImageBytes = loadedConfig.PDFMaxImageBytes
	pdfConsistencyContextMaxChars = loadedConfig.PDFContextMaxChars
	pdfToolPDFInfo = loadedConfig.PDFToolPDFInfo
	pdfToolPDFToPPM = loadedConfig.PDFToolPDFToPPM

	storageType = loadedConfig.StorageType
	s3Endpoint = loadedConfig.S3Endpoint
	s3Region = loadedConfig.S3Region
	s3Bucket = loadedConfig.S3Bucket
	s3AccessKeyID = loadedConfig.S3AccessKeyID
	s3SecretAccessKey = loadedConfig.S3SecretAccessKey
	s3UseSSL = loadedConfig.S3UseSSL

	// Keep existing validation as a safety net (also exercised by unit tests).
	if err := validatePDFConfigValues(); err != nil {
		return fmt.Errorf("%w (source: %s)", err, configPath)
	}

	return nil
}

// initStorageRuntime initializes the configured object storage or local filesystem driver.
func initStorageRuntime() error {
	if err := ensureConfigLoaded(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if storageType == "s3" {
		s3Cfg := storage.S3Config{
			Endpoint:        s3Endpoint,
			Region:          s3Region,
			Bucket:          s3Bucket,
			AccessKeyID:     s3AccessKeyID,
			SecretAccessKey: s3SecretAccessKey,
			UseSSL:          s3UseSSL,
		}
		s, err := storage.NewS3Storage(ctx, s3Cfg)
		if err != nil {
			return fmt.Errorf("init S3/MinIO storage failed: %w", err)
		}
		store.SetBlobStorage(s, "s3")
		procLogf("[BOOT] storage initialized: S3/MinIO endpoint=%s bucket=%s ssl=%v", s3Endpoint, s3Bucket, s3UseSSL)
		return nil
	}

	localDir := filepath.Join(projectRoot, ".run", "storage")
	s, err := storage.NewLocalStorage(localDir)
	if err != nil {
		return fmt.Errorf("init local storage failed: %w", err)
	}
	store.SetBlobStorage(s, "local")
	procLogf("[BOOT] storage initialized: local dir=%s", localDir)
	return nil
}

func effectiveJWTIssuer(base, mode string) string {
	base = strings.TrimSpace(base)
	if strings.EqualFold(strings.TrimSpace(mode), "DEV") {
		return base + ":dev"
	}
	return base
}

// validatePDFConfigValues checks PDF processing limits for invalid values.
func validatePDFConfigValues() error {
	if pdfMaxPages <= 0 {
		return fmt.Errorf("PDF_MAX_PAGES must be > 0")
	}
	if pdfMaxPagesPerRequest <= 0 {
		return fmt.Errorf("PDF_MAX_PAGES_PER_REQUEST must be > 0")
	}
	if pdfMaxPagesPerRequest > pdfMaxPages {
		return fmt.Errorf("PDF_MAX_PAGES_PER_REQUEST must be <= PDF_MAX_PAGES")
	}
	if pdfRenderDPI <= 0 {
		return fmt.Errorf("PDF_RENDER_DPI must be > 0")
	}
	if pdfBatchTimeoutSec <= 0 {
		return fmt.Errorf("PDF_BATCH_TIMEOUT_SEC must be > 0")
	}
	if pdfMaxRenderedImageBytes <= 0 {
		return fmt.Errorf("PDF_MAX_RENDERED_IMAGE_BYTES must be > 0")
	}
	if pdfConsistencyContextMaxChars <= 0 {
		return fmt.Errorf("PDF_CONSISTENCY_CONTEXT_MAX_CHARS must be > 0")
	}
	if pdfToolPDFInfo == "" {
		return fmt.Errorf("PDF_TOOL_PDFINFO must not be empty")
	}
	if pdfToolPDFToPPM == "" {
		return fmt.Errorf("PDF_TOOL_PDFTOPPM must not be empty")
	}
	return nil
}

// validatePDFTools verifies that required PDF command-line tools are installed.
func validatePDFTools() error {
	// Keep this signature stable (tests call it), but use the shared validator.
	c := config.Config{
		PDFToolPDFInfo:  strings.TrimSpace(pdfToolPDFInfo),
		PDFToolPDFToPPM: strings.TrimSpace(pdfToolPDFToPPM),
	}
	return c.ValidateExternalTools()
}

// appPort resolves the HTTP port from configuration.
func appPort() (string, error) {
	if err := ensureConfigLoaded(); err != nil {
		return "", err
	}
	if strings.TrimSpace(loadedConfig.Port) == "" {
		return "", fmt.Errorf("PORT must not be empty (source: %s)", configPath)
	}
	return strings.TrimSpace(loadedConfig.Port), nil
}

// geminiAPIKeysFromConfig returns the configured Gemini API keys.
func geminiAPIKeysFromConfig() []string {
	if err := ensureConfigLoaded(); err != nil {
		return nil
	}
	return loadedConfig.GeminiAPIKeys
}
