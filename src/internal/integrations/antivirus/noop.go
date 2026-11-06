package antivirus

import (
	"context"
	"io"
	"whisperserver/src/internal/domain"
)

// NoopScanner is used when antivirus scanning is disabled or in local development environments.
type NoopScanner struct{}

// NewNoopScanner creates a pass-through scanner.
func NewNoopScanner() *NoopScanner {
	return &NoopScanner{}
}

// Ping always succeeds.
func (n *NoopScanner) Ping(ctx context.Context) error {
	return nil
}

// ScanStream consumes the reader (to avoid dangling streams) and returns VerdictClean.
func (n *NoopScanner) ScanStream(ctx context.Context, r io.Reader, size int64) (domain.ScanResult, error) {
	// Discard read without buffering
	_, _ = io.Copy(io.Discard, r)
	return domain.ScanResult{
		Verdict:   domain.VerdictClean,
		EngineVer: "noop",
	}, nil
}

