package domain

import (
	"context"
	"io"
)

// ScanVerdict represents the malware scan outcome.
type ScanVerdict string

const (
	VerdictClean    ScanVerdict = "clean"
	VerdictInfected ScanVerdict = "infected"
	VerdictError    ScanVerdict = "error"
)

// ScanResult contains the outcome, detected signature, and scanner engine info.
type ScanResult struct {
	Verdict   ScanVerdict `json:"verdict"`
	Signature string      `json:"signature,omitempty"`
	EngineVer string      `json:"engine_ver,omitempty"`
}

// MalwareScanner scans streaming file contents for malicious payloads.
type MalwareScanner interface {
	ScanStream(ctx context.Context, r io.Reader, size int64) (ScanResult, error)
	Ping(ctx context.Context) error
}

