package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	intutil "whisperserver/src/internal/util"
)

var (
	configOnce   sync.Once
	configErr    error
	configPath   string
	configValues map[string][]string
)

func ensureConfigLoaded() error {
	configOnce.Do(func() {
		configValues, configPath, configErr = loadConfigFile()
	})
	return configErr
}

func loadConfigFile() (map[string][]string, string, error) {
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

	values := map[string][]string{}
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

func confString(key string) string {
	if err := ensureConfigLoaded(); err != nil {
		return ""
	}
	vals := configValues[key]
	if len(vals) == 0 {
		return ""
	}
	v := strings.TrimSpace(vals[len(vals)-1])
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
		if s, err := strconv.Unquote(v); err == nil {
			return strings.TrimSpace(s)
		}
	}
	if strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") && len(v) >= 2 {
		return strings.TrimSpace(v[1 : len(v)-1])
	}
	return v
}

func confInt(key string) int {
	v := confString(key)
	if v == "" {
		return 0
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return i
}

func confBool(key string) bool {
	return intutil.Truthy(confString(key))
}

func confList(key string) []string {
	if err := ensureConfigLoaded(); err != nil {
		return nil
	}
	rawValues := configValues[key]
	if len(rawValues) == 0 {
		return nil
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(rawValues))
	add := func(v string) {
		v = strings.TrimSpace(v)
		v = strings.Trim(v, "\"")
		v = strings.Trim(v, "'")
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}

	for _, raw := range rawValues {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
			var parsed []string
			if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
				for _, p := range parsed {
					add(p)
				}
				continue
			}
			raw = strings.TrimPrefix(raw, "[")
			raw = strings.TrimSuffix(raw, "]")
		}

		for _, line := range strings.Split(raw, "\n") {
			for _, part := range strings.Split(line, ",") {
				add(part)
			}
		}
	}
	return out
}

func initRuntimeConfig() error {
	if err := ensureConfigLoaded(); err != nil {
		return err
	}

	maxUploadSizeMB = confInt("MAX_UPLOAD_SIZE_MB")
	if maxUploadSizeMB <= 0 {
		return fmt.Errorf("MAX_UPLOAD_SIZE_MB must be > 0 (source: %s)", configPath)
	}

	uploadRateLimitKB = confInt("UPLOAD_RATE_LIMIT_KBPS")
	if uploadRateLimitKB < 0 {
		return fmt.Errorf("UPLOAD_RATE_LIMIT_KBPS must be >= 0 (source: %s)", configPath)
	}

	jobTimeoutSec = confInt("JOB_TIMEOUT_SEC")
	if jobTimeoutSec <= 0 {
		return fmt.Errorf("JOB_TIMEOUT_SEC must be > 0 (source: %s)", configPath)
	}
	splitTaskQueues = confBool("SPLIT_TRANSCRIBE_REFINE_QUEUE")

	geminiModel = strings.TrimSpace(confString("GEMINI_MODEL"))
	if geminiModel == "" {
		return fmt.Errorf("GEMINI_MODEL must not be empty (source: %s)", configPath)
	}

	jwtSecret = []byte(strings.TrimSpace(confString("JWT_SECRET")))
	jwtIssuer = strings.TrimSpace(confString("JWT_ISSUER"))
	if jwtIssuer == "" {
		return fmt.Errorf("JWT_ISSUER must not be empty (source: %s)", configPath)
	}

	jwtExpiryHours = confInt("JWT_EXP_HOURS")
	if jwtExpiryHours <= 0 {
		return fmt.Errorf("JWT_EXP_HOURS must be > 0 (source: %s)", configPath)
	}
	authCookieSecure = confBool("AUTH_COOKIE_SECURE")

	pdfMaxPages = confInt("PDF_MAX_PAGES")
	pdfMaxPagesPerRequest = confInt("PDF_MAX_PAGES_PER_REQUEST")
	pdfRenderDPI = confInt("PDF_RENDER_DPI")
	pdfBatchTimeoutSec = confInt("PDF_BATCH_TIMEOUT_SEC")
	pdfMaxRenderedImageBytes = int64(confInt("PDF_MAX_RENDERED_IMAGE_BYTES"))
	pdfConsistencyContextMaxChars = confInt("PDF_CONSISTENCY_CONTEXT_MAX_CHARS")
	pdfToolPDFInfo = strings.TrimSpace(confString("PDF_TOOL_PDFINFO"))
	pdfToolPDFToPPM = strings.TrimSpace(confString("PDF_TOOL_PDFTOPPM"))
	if err := validatePDFConfigValues(); err != nil {
		return fmt.Errorf("%w (source: %s)", err, configPath)
	}

	return nil
}

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

func validatePDFTools() error {
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
	if err := check("pdfinfo", pdfToolPDFInfo, "PDF_TOOL_PDFINFO"); err != nil {
		return err
	}
	if err := check("pdftoppm", pdfToolPDFToPPM, "PDF_TOOL_PDFTOPPM"); err != nil {
		return err
	}
	return nil
}

func appPort() (string, error) {
	port := strings.TrimSpace(confString("PORT"))
	if port == "" {
		return "", fmt.Errorf("PORT must not be empty (source: %s)", configPath)
	}
	return port, nil
}

func geminiAPIKeysFromConfig() []string {
	return confList("GEMINI_API_KEYS")
}
