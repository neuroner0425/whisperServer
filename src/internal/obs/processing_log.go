package obs

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
)

// ProcessingLog writes the long-lived processing log used for operational tracing.
type ProcessingLog struct {
	path   string
	file   *os.File
	log    *log.Logger
	slog   *slog.Logger
}

// NewProcessingLog opens the shared processing log file under the project root.
func NewProcessingLog(projectRoot string) (*ProcessingLog, error) {
	p := &ProcessingLog{
		path: filepath.Join(projectRoot, "log", "processing.log"),
		log:  log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds),
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	p.file = f
	mw := io.MultiWriter(os.Stdout, f)
	p.log = log.New(mw, "", log.LstdFlags|log.Lmicroseconds)

	// Configure root structured slog with MultiWriter so JSON logs are captured in file & stdout
	p.slog = InitLogger(mw, slog.LevelInfo)

	return p, nil
}

// Close releases the log file handle.
func (p *ProcessingLog) Close() {
	if p == nil || p.file == nil {
		return
	}
	_ = p.file.Close()
	p.file = nil
}

// Slog returns the structured slog.Logger instance.
func (p *ProcessingLog) Slog() *slog.Logger {
	if p == nil || p.slog == nil {
		return GetLogger()
	}
	return p.slog
}

// Logf writes one formatted log line.
func (p *ProcessingLog) Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if p != nil && p.log != nil {
		p.log.Print(msg)
	}
	GetLogger().Info(msg)
}

// Errf writes one formatted error log line with an optional cause.
func (p *ProcessingLog) Errf(scope string, err error, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if p != nil && p.log != nil {
		if err == nil {
			p.log.Printf("[ERROR] %s: %s", scope, msg)
		} else {
			p.log.Printf("[ERROR] %s: %s | cause=%v", scope, msg, err)
		}
	}
	if err != nil {
		GetLogger().Error(msg, slog.String("scope", scope), slog.Any("error", err))
	} else {
		GetLogger().Error(msg, slog.String("scope", scope))
	}
}
