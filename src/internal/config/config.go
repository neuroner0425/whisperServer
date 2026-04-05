package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	intutil "whisperserver/src/internal/util"
)

// Config is the runtime configuration loaded from app.conf (or app.conf.default).
// Keep this free of any app-wide globals so it can be injected into other modules.
type Config struct {
	SourcePath string

	Port string

	MaxUploadSizeMB      int
	UploadRateLimitKBPS  int
	JobTimeoutSec        int
	SplitTaskQueues      bool
	GeminiModel          string
	GeminiAPIKeys        []string
	JWTSecret            []byte
	JWTIssuer            string
	JWTExpiryHours       int
	AuthCookieSecure     bool
	PDFMaxPages          int
	PDFMaxPagesPerReq    int
	PDFRenderDPI         int
	PDFBatchTimeoutSec   int
	PDFMaxImageBytes     int64
	PDFContextMaxChars   int
	PDFToolPDFInfo       string
	PDFToolPDFToPPM      string
}

type rawValues map[string][]string

func Load(projectRoot string) (Config, error) {
	values, srcPath, err := loadConfigFile(projectRoot)
	if err != nil {
		return Config{}, err
	}
	c := Config{SourcePath: srcPath}

	c.Port = strings.TrimSpace(values.string("PORT"))
	if c.Port == "" {
		return Config{}, fmt.Errorf("PORT must not be empty (source: %s)", srcPath)
	}

	c.MaxUploadSizeMB = values.int("MAX_UPLOAD_SIZE_MB")
	if c.MaxUploadSizeMB <= 0 {
		return Config{}, fmt.Errorf("MAX_UPLOAD_SIZE_MB must be > 0 (source: %s)", srcPath)
	}

	c.UploadRateLimitKBPS = values.int("UPLOAD_RATE_LIMIT_KBPS")
	if c.UploadRateLimitKBPS < 0 {
		return Config{}, fmt.Errorf("UPLOAD_RATE_LIMIT_KBPS must be >= 0 (source: %s)", srcPath)
	}

	c.JobTimeoutSec = values.int("JOB_TIMEOUT_SEC")
	if c.JobTimeoutSec <= 0 {
		return Config{}, fmt.Errorf("JOB_TIMEOUT_SEC must be > 0 (source: %s)", srcPath)
	}
	c.SplitTaskQueues = values.bool("SPLIT_TRANSCRIBE_REFINE_QUEUE")

	c.GeminiModel = strings.TrimSpace(values.string("GEMINI_MODEL"))
	if c.GeminiModel == "" {
		return Config{}, fmt.Errorf("GEMINI_MODEL must not be empty (source: %s)", srcPath)
	}
	c.GeminiAPIKeys = values.list("GEMINI_API_KEYS")

	c.JWTSecret = []byte(strings.TrimSpace(values.string("JWT_SECRET")))
	c.JWTIssuer = strings.TrimSpace(values.string("JWT_ISSUER"))
	if c.JWTIssuer == "" {
		return Config{}, fmt.Errorf("JWT_ISSUER must not be empty (source: %s)", srcPath)
	}
	c.JWTExpiryHours = values.int("JWT_EXP_HOURS")
	if c.JWTExpiryHours <= 0 {
		return Config{}, fmt.Errorf("JWT_EXP_HOURS must be > 0 (source: %s)", srcPath)
	}
	c.AuthCookieSecure = values.bool("AUTH_COOKIE_SECURE")

	c.PDFMaxPages = values.int("PDF_MAX_PAGES")
	c.PDFMaxPagesPerReq = values.int("PDF_MAX_PAGES_PER_REQUEST")
	c.PDFRenderDPI = values.int("PDF_RENDER_DPI")
	c.PDFBatchTimeoutSec = values.int("PDF_BATCH_TIMEOUT_SEC")
	c.PDFMaxImageBytes = int64(values.int("PDF_MAX_RENDERED_IMAGE_BYTES"))
	c.PDFContextMaxChars = values.int("PDF_CONSISTENCY_CONTEXT_MAX_CHARS")
	c.PDFToolPDFInfo = strings.TrimSpace(values.string("PDF_TOOL_PDFINFO"))
	c.PDFToolPDFToPPM = strings.TrimSpace(values.string("PDF_TOOL_PDFTOPPM"))

	if err := c.validatePDFValues(); err != nil {
		return Config{}, fmt.Errorf("%w (source: %s)", err, srcPath)
	}

	return c, nil
}

func (c Config) validatePDFValues() error {
	if c.PDFMaxPages <= 0 {
		return fmt.Errorf("PDF_MAX_PAGES must be > 0")
	}
	if c.PDFMaxPagesPerReq <= 0 {
		return fmt.Errorf("PDF_MAX_PAGES_PER_REQUEST must be > 0")
	}
	if c.PDFMaxPagesPerReq > c.PDFMaxPages {
		return fmt.Errorf("PDF_MAX_PAGES_PER_REQUEST must be <= PDF_MAX_PAGES")
	}
	if c.PDFRenderDPI <= 0 {
		return fmt.Errorf("PDF_RENDER_DPI must be > 0")
	}
	if c.PDFBatchTimeoutSec <= 0 {
		return fmt.Errorf("PDF_BATCH_TIMEOUT_SEC must be > 0")
	}
	if c.PDFMaxImageBytes <= 0 {
		return fmt.Errorf("PDF_MAX_RENDERED_IMAGE_BYTES must be > 0")
	}
	if c.PDFContextMaxChars <= 0 {
		return fmt.Errorf("PDF_CONSISTENCY_CONTEXT_MAX_CHARS must be > 0")
	}
	if c.PDFToolPDFInfo == "" {
		return fmt.Errorf("PDF_TOOL_PDFINFO must not be empty")
	}
	if c.PDFToolPDFToPPM == "" {
		return fmt.Errorf("PDF_TOOL_PDFTOPPM must not be empty")
	}
	return nil
}

// ValidateExternalTools verifies external tool configuration (pdfinfo/pdftoppm).
func (c Config) ValidateExternalTools() error {
	check := func(label, tool, configKey string) error {
		if strings.Contains(tool, "/") {
			if _, err := os.Stat(tool); err != nil {
				return fmt.Errorf("%s tool not found: %s (config key: %s)", label, tool, configKey)
			}
			return nil
		}
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s tool not found in PATH: %s (config key: %s)", label, tool, configKey)
		}
		return nil
	}
	if err := check("pdfinfo", c.PDFToolPDFInfo, "PDF_TOOL_PDFINFO"); err != nil {
		return err
	}
	if err := check("pdftoppm", c.PDFToolPDFToPPM, "PDF_TOOL_PDFTOPPM"); err != nil {
		return err
	}
	return nil
}

func loadConfigFile(projectRoot string) (rawValues, string, error) {
	userPath := filepath.Join(projectRoot, "app.conf")
	defaultPath := filepath.Join(projectRoot, "app.conf.default")
	targetPath := defaultPath
	if intutil.FileExists(userPath) {
		targetPath = userPath
	}

	b, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read config file %s: %w", targetPath, err)
	}

	values := rawValues{}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	var pendingKey string
	var pendingValue strings.Builder
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if pendingKey != "" {
			pendingValue.WriteByte('\n')
			pendingValue.WriteString(line)
			if strings.Contains(line, "]") {
				values[pendingKey] = append(values[pendingKey], strings.TrimSpace(pendingValue.String()))
				pendingKey = ""
				pendingValue.Reset()
			}
			continue
		}

		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, "", fmt.Errorf("invalid config line: %q", line)
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		if key == "" {
			return nil, "", fmt.Errorf("empty config key in line: %q", line)
		}

		if strings.HasPrefix(value, "[") && !strings.Contains(value, "]") {
			pendingKey = key
			pendingValue.WriteString(value)
			continue
		}
		values[key] = append(values[key], value)
	}
	if err := sc.Err(); err != nil {
		return nil, "", err
	}
	if pendingKey != "" {
		return nil, "", fmt.Errorf("unterminated list value for key %s", pendingKey)
	}
	return values, targetPath, nil
}

func (v rawValues) string(key string) string {
	vals := v[key]
	if len(vals) == 0 {
		return ""
	}
	s := strings.TrimSpace(vals[len(vals)-1])
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		if unq, err := strconv.Unquote(s); err == nil {
			return strings.TrimSpace(unq)
		}
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func (v rawValues) int(key string) int {
	s := v.string(key)
	if s == "" {
		return 0
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return i
}

func (v rawValues) bool(key string) bool {
	return intutil.Truthy(v.string(key))
}

func (v rawValues) list(key string) []string {
	raw := v[key]
	if len(raw) == 0 {
		return nil
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(raw))
	add := func(s string) {
		s = strings.TrimSpace(s)
		s = strings.Trim(s, "\"")
		s = strings.Trim(s, "'")
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}

	for _, rawValue := range raw {
		rawValue = strings.TrimSpace(rawValue)
		if rawValue == "" {
			continue
		}

		if strings.HasPrefix(rawValue, "[") && strings.HasSuffix(rawValue, "]") {
			var parsed []string
			if err := json.Unmarshal([]byte(rawValue), &parsed); err == nil {
				for _, p := range parsed {
					add(p)
				}
				continue
			}
			rawValue = strings.TrimPrefix(rawValue, "[")
			rawValue = strings.TrimSuffix(rawValue, "]")
		}

		for _, line := range strings.Split(rawValue, "\n") {
			for _, part := range strings.Split(line, ",") {
				add(part)
			}
		}
	}
	return out
}

