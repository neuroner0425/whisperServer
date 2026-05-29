package server

import (
	"os"
	"testing"
)

func TestStartupVADFallbackRequeueEnabled(t *testing.T) {
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	os.Args = []string{"server"}
	if startupVADFallbackRequeueEnabled() {
		t.Fatal("expected VAD fallback startup requeue to be disabled by default")
	}

	os.Args = []string{"server", "--requeue-vad-fallback"}
	if !startupVADFallbackRequeueEnabled() {
		t.Fatal("expected VAD fallback startup requeue to be enabled by explicit arg")
	}
}
