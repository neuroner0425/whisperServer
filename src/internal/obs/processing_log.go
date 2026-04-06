package obs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// ProcessingLog writes the long-lived processing log used for operational tracing.
type ProcessingLog struct {
	path string
	file *os.File
	log  *log.Logger
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
	p.log = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
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

// Logf writes one formatted log line.
func (p *ProcessingLog) Logf(format string, args ...any) {
	if p == nil || p.log == nil {
		return
	}
	p.log.Printf(format, args...)
}

// Errf writes one formatted error log line with an optional cause.
func (p *ProcessingLog) Errf(scope string, err error, format string, args ...any) {
	if err == nil {
		p.Logf("[ERROR] %s: %s", scope, fmt.Sprintf(format, args...))
		return
	}
	p.Logf("[ERROR] %s: %s | cause=%v", scope, fmt.Sprintf(format, args...), err)
}
