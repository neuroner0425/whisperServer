package server

import (
	"fmt"
	"sync"

	"strings"

	"whisperserver/src/internal/config"
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
	splitTaskQueues = loadedConfig.SplitTaskQueues

	geminiModel = loadedConfig.GeminiModel

	jwtSecret = loadedConfig.JWTSecret
	jwtIssuer = loadedConfig.JWTIssuer
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

	// Keep existing validation as a safety net (also exercised by unit tests).
	if err := validatePDFConfigValues(); err != nil {
		return fmt.Errorf("%w (source: %s)", err, configPath)
	}

	return nil
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
