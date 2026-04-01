package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

var (
	processingLogPath = filepath.Join(projectRoot, "log", "processing.log")
	processingLogFile *os.File
	processingLogger  = log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)
)

func initProcessingLogger() error {
	if err := os.MkdirAll(filepath.Dir(processingLogPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(processingLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	processingLogFile = f
	processingLogger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	return nil
}

func closeProcessingLogger() {
	if processingLogFile != nil {
		_ = processingLogFile.Close()
		processingLogFile = nil
	}
}

func procLogf(format string, args ...any) {
	processingLogger.Printf(format, args...)
}

func procErrf(scope string, err error, format string, args ...any) {
	if err == nil {
		procLogf("[ERROR] %s: %s", scope, fmt.Sprintf(format, args...))
		return
	}
	procLogf("[ERROR] %s: %s | cause=%v", scope, fmt.Sprintf(format, args...), err)
}
