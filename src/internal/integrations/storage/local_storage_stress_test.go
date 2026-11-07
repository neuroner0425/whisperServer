package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStress_LocalStorage_PathTraversalMatrix tests path traversal attack vectors
// against all public methods of LocalStorage.
func TestStress_LocalStorage_PathTraversalMatrix(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewLocalStorage(baseDir, WithSecretKey("stress-secret-key-storage-matrix"))
	if err != nil {
		t.Fatalf("NewLocalStorage failed: %v", err)
	}

	// Create a canary file outside baseDir to verify it cannot be read, overwritten, or deleted
	canaryDir := t.TempDir()
	canaryPath := filepath.Join(canaryDir, "secret_canary.txt")
	canaryContent := []byte("TOP_SECRET_HOST_CANARY")
	if err := os.WriteFile(canaryPath, canaryContent, 0o600); err != nil {
		t.Fatalf("write canary failed: %v", err)
	}

	traversalVectors := []string{
		"../",
		"..%2F",
		"/../../",
		"jobs/../../etc/passwd",
		"....//",
		"..",
		"../../../../../../etc/shadow",
		"a/b/../../../etc/hosts",
		"jobs/../jobs/../../secret",
		"jobs/..\\..\\windows\\system32",
		"..\\..\\test",
		"/tmp/escape",
		"\\escape",
	}

	ctx := context.Background()

	for _, vector := range traversalVectors {
		t.Run("Vector:"+vector, func(t *testing.T) {
			// 1. Put
			_, errPut := store.Put(ctx, vector, []byte("injected-data"), "text/plain")
			if strings.Contains(vector, "..") && errPut == nil {
				t.Fatalf("Put(%q) expected error, got nil", vector)
			}

			// 2. PutStream
			_, errPutStream := store.PutStream(ctx, vector, bytes.NewReader([]byte("injected-stream")), 15, "text/plain")
			if strings.Contains(vector, "..") && errPutStream == nil {
				t.Fatalf("PutStream(%q) expected error, got nil", vector)
			}

			// 3. Get
			_, errGet := store.Get(ctx, vector)
			if strings.Contains(vector, "..") && errGet == nil {
				t.Fatalf("Get(%q) expected error, got nil", vector)
			}

			// 4. GetStream
			rc, _, errGetStream := store.GetStream(ctx, vector)
			if strings.Contains(vector, "..") && errGetStream == nil {
				_ = rc.Close()
				t.Fatalf("GetStream(%q) expected error, got nil", vector)
			}

			// 5. GetReadSeeker
			rsc, _, errGetRS := store.GetReadSeeker(ctx, vector)
			if strings.Contains(vector, "..") && errGetRS == nil {
				_ = rsc.Close()
				t.Fatalf("GetReadSeeker(%q) expected error, got nil", vector)
			}

			// 6. Delete
			errDel := store.Delete(ctx, vector)
			if strings.Contains(vector, "..") && errDel == nil {
				t.Fatalf("Delete(%q) expected error, got nil", vector)
			}

			// 7. DeletePrefix
			errDelP := store.DeletePrefix(ctx, vector)
			if strings.Contains(vector, "..") && errDelP == nil {
				t.Fatalf("DeletePrefix(%q) expected error, got nil", vector)
			}

			// 8. Exists
			exists, errExists := store.Exists(ctx, vector)
			if strings.Contains(vector, "..") && errExists == nil {
				t.Fatalf("Exists(%q) expected error, got nil (exists=%v)", vector, exists)
			}

			// 9. Stat
			_, errStat := store.Stat(ctx, vector)
			if strings.Contains(vector, "..") && errStat == nil {
				t.Fatalf("Stat(%q) expected error, got nil", vector)
			}

			// 10. PresignPut
			_, errPP := store.PresignPut(ctx, vector, "audio/mp4", 10*time.Minute)
			if strings.Contains(vector, "..") && errPP == nil {
				t.Fatalf("PresignPut(%q) expected error, got nil", vector)
			}

			// 11. PresignGet
			_, errPG := store.PresignGet(ctx, vector, "test.mp4", 10*time.Minute)
			if strings.Contains(vector, "..") && errPG == nil {
				t.Fatalf("PresignGet(%q) expected error, got nil", vector)
			}

			// 12. VerifyLocalToken
			future := time.Now().Add(1 * time.Hour).Unix()
			validToken := store.VerifyLocalToken("get", vector, "test.mp4", future, "anysig")
			if strings.Contains(vector, "..") && validToken {
				t.Fatalf("VerifyLocalToken(%q) returned true, expected false", vector)
			}
		})
	}

	// Verify canary file remains untouched
	canaryAfter, err := os.ReadFile(canaryPath)
	if err != nil || !bytes.Equal(canaryAfter, canaryContent) {
		t.Fatalf("CRITICAL SECURITY FAILURE: external canary file was modified or deleted! err=%v", err)
	}
}
