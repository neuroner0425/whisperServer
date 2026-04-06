// process_log.go adapts the obs processing logger into bootstrap-local helpers.
package server

import (
	"whisperserver/src/internal/obs"
)

var (
	procLogger *obs.ProcessingLog
)

// initProcessingLogger opens the process log before other services start.
func initProcessingLogger() error {
	l, err := obs.NewProcessingLog(projectRoot)
	if err != nil {
		return err
	}
	procLogger = l
	return nil
}

// closeProcessingLogger closes the shared process log during shutdown.
func closeProcessingLogger() {
	if procLogger != nil {
		procLogger.Close()
		procLogger = nil
	}
}

// procLogf writes an informational message when the processing logger is available.
func procLogf(format string, args ...any) {
	if procLogger != nil {
		procLogger.Logf(format, args...)
	}
}

// procErrf writes a structured error message when the processing logger is available.
func procErrf(scope string, err error, format string, args ...any) {
	if procLogger != nil {
		procLogger.Errf(scope, err, format, args...)
	}
}
