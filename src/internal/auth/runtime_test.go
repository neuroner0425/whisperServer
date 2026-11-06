package auth

import (
	"os"
	"os/exec"
	"testing"
)

func TestNewRuntime_ProductionSecretTooShort_FailsFast(t *testing.T) {
	if os.Getenv("BE_CRASHER") == "1" {
		_ = New(Config{
			IsProduction: true,
			JWTSecret:    []byte("short-secret"),
		}, nil, nil)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestNewRuntime_ProductionSecretTooShort_FailsFast")
	cmd.Env = append(os.Environ(), "BE_CRASHER=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return // Expected fatal exit
	}
	t.Fatalf("process ran with err %v, want exit status != 0", err)
}

func TestNewRuntime_ProductionValidSecret_Succeeds(t *testing.T) {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	rt := New(Config{
		IsProduction:     true,
		JWTSecret:        secret,
		AuthCookieSecure: false, // should be overridden to true in production
	}, nil, nil)
	if rt == nil {
		t.Fatal("expected non-nil Runtime")
	}
}
