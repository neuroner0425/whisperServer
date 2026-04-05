package obs

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type ProcessingLog struct {
	path string
	file *os.File
	log  *log.Logger
}

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

func (p *ProcessingLog) Close() {
	if p == nil || p.file == nil {
		return
	}
	_ = p.file.Close()
	p.file = nil
}

func (p *ProcessingLog) Logf(format string, args ...any) {
	if p == nil || p.log == nil {
		return
	}
	p.log.Printf(format, args...)
}

func (p *ProcessingLog) Errf(scope string, err error, format string, args ...any) {
	if err == nil {
		p.Logf("[ERROR] %s: %s", scope, fmt.Sprintf(format, args...))
		return
	}
	p.Logf("[ERROR] %s: %s | cause=%v", scope, fmt.Sprintf(format, args...), err)
}

