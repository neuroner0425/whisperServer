package sqlite

import (
	"context"
	"strings"
	"testing"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
)

func TestCompatMigrationSuite(t *testing.T) {
	projectRoot := t.TempDir()
	t.Cleanup(func() {
		Close()
	})

	if err := Init(projectRoot); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// 1. Setup sample user and jobs
	_, _ = dbConn.Exec(`INSERT INTO users(id, login_id, email, password_hash) VALUES ('u1', 'user1', 'u1@example.com', 'hash')`)
	job := &model.Job{
		StatusCode: 50,
		Filename:   "test.mp3",
		FileType:   "audio",
		OwnerID:    "u1",
	}
	if err := SaveJob("job-1", job); err != nil {
		t.Fatalf("SaveJob: %v", err)
	}

	// 2. Insert uncompressed raw JSON (larger string so gzip compression actually saves bytes)
	rawJSON := strings.Repeat(`{"sample": "uncompressed text payload that should be compressed with gzip to save space"}, `, 20)
	_, err := dbConn.Exec(`INSERT INTO job_json(job_id, kind, data) VALUES ('job-1', 'transcript_json', ?)`, rawJSON)
	if err != nil {
		t.Fatalf("insert raw job_json: %v", err)
	}

	// Insert orphan JSON (disable FK temporarily)
	_, _ = dbConn.Exec(`PRAGMA foreign_keys = OFF;`)
	_, err = dbConn.Exec(`INSERT INTO job_json(job_id, kind, data) VALUES ('orphan-job', 'transcript_json', 'orphan')`)
	if err != nil {
		t.Fatalf("insert orphan job_json: %v", err)
	}
	_, _ = dbConn.Exec(`PRAGMA foreign_keys = ON;`)
	// Insert transient diagnostic
	_, _ = dbConn.Exec(`INSERT INTO job_json(job_id, kind, data) VALUES ('job-1', 'refine_diagnostics_123', '{"d":1}')`)

	// 3. Test CheckJSONCompression
	report, err := CheckJSONCompression(dbConn)
	if err != nil {
		t.Fatalf("CheckJSONCompression: %v", err)
	}
	if report.UncompressedCount < 2 {
		t.Fatalf("expected at least 2 uncompressed JSONs, got %d", report.UncompressedCount)
	}

	// 4. Test CheckOrphanRecords
	orphans, err := CheckOrphanRecords(dbConn)
	if err != nil {
		t.Fatalf("CheckOrphanRecords: %v", err)
	}
	if orphans.OrphanJSONCount != 1 {
		t.Fatalf("expected 1 orphan JSON, got %d", orphans.OrphanJSONCount)
	}
	if orphans.TransientKinds != 1 {
		t.Fatalf("expected 1 transient kind, got %d", orphans.TransientKinds)
	}

	// 5. Test CleanupOrphanAndTransientRecords
	deleted, err := CleanupOrphanAndTransientRecords(dbConn)
	if err != nil {
		t.Fatalf("CleanupOrphanAndTransientRecords: %v", err)
	}
	if deleted < 2 {
		t.Fatalf("expected at least 2 records deleted, got %d", deleted)
	}

	// 6. Test CompressUncompressedJobJSON
	migrated, saved, err := CompressUncompressedJobJSON(dbConn, nil)
	if err != nil {
		t.Fatalf("CompressUncompressedJobJSON: %v", err)
	}
	if migrated != 1 {
		t.Fatalf("expected 1 job_json compressed, got %d", migrated)
	}
	if saved <= 0 {
		t.Fatalf("expected saved bytes > 0, got %d", saved)
	}

	// Verify it can now be loaded cleanly with LoadJobJSON
	loaded, err := LoadJobJSON("job-1", "transcript_json")
	if err != nil {
		t.Fatalf("LoadJobJSON error: %v", err)
	}
	if loaded != rawJSON {
		t.Fatalf("expected loaded to match rawJSON, got %q", loaded)
	}

	// Check compression report again
	postReport, err := CheckJSONCompression(dbConn)
	if err != nil {
		t.Fatalf("post CheckJSONCompression: %v", err)
	}
	if postReport.UncompressedCount != 0 {
		t.Fatalf("expected 0 uncompressed JSONs after migration, got %d", postReport.UncompressedCount)
	}

	// 7. Test Media Storage Health with MemoryStorage
	memStore := storage.NewMemoryStorage()
	_, _ = memStore.PutStream(context.Background(), "jobs/job-1/audio_aac", strings.NewReader("audio"), 5, "audio/mp4")
	_ = SaveJobMediaMetadata("job-1", "audio_aac", "memory", "jobs/job-1/audio_aac", 5, "audio/mp4", "etag")

	mediaReport, err := CheckMediaStorageHealth(context.Background(), dbConn, memStore)
	if err != nil {
		t.Fatalf("CheckMediaStorageHealth: %v", err)
	}
	if mediaReport.JobMediaCount != 1 || mediaReport.VerifiedPresentCount != 1 || mediaReport.MissingObjectsCount != 0 {
		t.Fatalf("unexpected mediaReport: %+v", mediaReport)
	}

	// 8. Test Status Integrity
	validCodes, invalidCodes, _, err := CheckJobsStatusIntegrity(dbConn)
	if err != nil {
		t.Fatalf("CheckJobsStatusIntegrity: %v", err)
	}
	if validCodes != 1 || invalidCodes != 0 {
		t.Fatalf("expected 1 valid code, 0 invalid, got valid=%d, invalid=%d", validCodes, invalidCodes)
	}

	// 9. Test EnsureDatabaseIndexes
	if err := EnsureDatabaseIndexes(dbConn); err != nil {
		t.Fatalf("EnsureDatabaseIndexes: %v", err)
	}
}
