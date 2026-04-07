package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Runtime artifacts are transient job-local files. They are intentionally kept
// outside SQLite so they can be cleaned up independently from durable source
// blobs and durable JSON results.

func isRuntimeArtifactKind(kind string) bool {
	return kind == BlobKindPreview || kind == BlobKindDocumentChunkIndex || strings.HasPrefix(kind, "document_chunk_")
}

func runtimeArtifactJobDir(jobID string) string {
	return filepath.Join(runtimeArtifactsDir, jobID)
}

func runtimeArtifactPath(jobID, kind string) string {
	safeKind := strings.ReplaceAll(kind, "/", "_")
	return filepath.Join(runtimeArtifactJobDir(jobID), safeKind)
}

// SaveJobRuntimeArtifact stores one transient runtime artifact on disk.
func SaveJobRuntimeArtifact(jobID, kind string, data []byte) error {
	if !isRuntimeArtifactKind(kind) {
		return fmt.Errorf("not a runtime artifact kind: %s", kind)
	}
	dir := runtimeArtifactJobDir(jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(runtimeArtifactPath(jobID, kind), data, 0o644)
}

// LoadJobRuntimeArtifact loads one transient runtime artifact from disk.
func LoadJobRuntimeArtifact(jobID, kind string) ([]byte, error) {
	return os.ReadFile(runtimeArtifactPath(jobID, kind))
}

// HasJobRuntimeArtifact reports whether a transient runtime artifact exists.
func HasJobRuntimeArtifact(jobID, kind string) bool {
	_, err := LoadJobRuntimeArtifact(jobID, kind)
	return err == nil
}

// DeleteJobRuntimeArtifact removes one transient runtime artifact from disk.
func DeleteJobRuntimeArtifact(jobID, kind string) {
	err := os.Remove(runtimeArtifactPath(jobID, kind))
	if err != nil && !os.IsNotExist(err) {
		logf("[DB] runtime artifact delete failed job_id=%s kind=%s err=%v", jobID, kind, err)
	}
}

// DeleteAllJobRuntimeArtifacts removes every transient runtime artifact for one job.
func DeleteAllJobRuntimeArtifacts(jobID string) {
	_ = os.RemoveAll(runtimeArtifactJobDir(jobID))
}

// ListJobRuntimeArtifactKinds lists transient artifact filenames for one job.
func ListJobRuntimeArtifactKinds(jobID string) []string {
	entries, err := os.ReadDir(runtimeArtifactJobDir(jobID))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		out = append(out, entry.Name())
	}
	return out
}
