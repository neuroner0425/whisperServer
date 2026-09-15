package sqlite

import (
	"context"
	"testing"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
)

func TestJobMediaAndBlobStorage(t *testing.T) {
	tmpDir := t.TempDir()
	if err := InitWithFilename(tmpDir, "test_media.db"); err != nil {
		t.Fatalf("init db: %v", err)
	}

	// 1. Setup in-memory storage driver
	memStorage := storage.NewMemoryStorage()
	SetBlobStorage(memStorage, "memory")
	defer SetBlobStorage(nil, "s3")

	// 2. Create user and job
	if _, err := dbConn.Exec(`INSERT INTO users(id, login_id, email, password_hash) VALUES ('user-test', 'user-test', 'test@example.com', 'hash')`); err != nil {
		t.Fatalf("create user: %v", err)
	}
	jobID := "job-test-1"
	job := &model.Job{
		OwnerID:    "user-test",
		Filename:   "audio.mp3",
		FileType:   "audio",
		StatusCode: model.JobStatusPendingCode,
	}
	if err := SaveJob(jobID, job); err != nil {
		t.Fatalf("save job: %v", err)
	}

	// 3. Save audio AAC blob
	audioData := []byte("audio-aac-binary-contents-12345")
	if err := SaveJobBlob(jobID, BlobKindAudioAAC, audioData); err != nil {
		t.Fatalf("save audio blob: %v", err)
	}

	// 4. Verify job_media record was created
	media, err := GetJobMediaMetadata(jobID, BlobKindAudioAAC)
	if err != nil || media == nil {
		t.Fatalf("expected job_media record, got err=%v media=%v", err, media)
	}
	if media.SizeBytes != int64(len(audioData)) {
		t.Fatalf("expected size %d, got %d", len(audioData), media.SizeBytes)
	}
	if media.ContentType != "audio/mp4" {
		t.Fatalf("expected audio/mp4, got %s", media.ContentType)
	}

	// 5. Verify object exists in storage
	stored, err := memStorage.Get(context.Background(), media.StorageKey)
	if err != nil {
		t.Fatalf("get from storage: %v", err)
	}
	if string(stored) != string(audioData) {
		t.Fatalf("storage data mismatch")
	}

	// 6. Verify LoadJobBlob retrieves the data seamlessly
	loaded, err := LoadJobBlob(jobID, BlobKindAudioAAC)
	if err != nil {
		t.Fatalf("load blob: %v", err)
	}
	if string(loaded) != string(audioData) {
		t.Fatalf("loaded data mismatch")
	}

	// 7. Verify ListJobBlobUsageByOwner calculates storage from job_media
	usages, err := ListJobBlobUsageByOwner("user-test")
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	if len(usages) != 1 || usages[0].Bytes != int64(len(audioData)) {
		t.Fatalf("expected usage bytes %d, got %v", len(audioData), usages)
	}

	// 7b. Verify ListStorageJobItemsByOwner returns full storage items with job metadata
	storageItems, err := ListStorageJobItemsByOwner("user-test")
	if err != nil {
		t.Fatalf("list storage items: %v", err)
	}
	if len(storageItems) != 1 || storageItems[0].JobID != jobID || storageItems[0].SizeBytes != int64(len(audioData)) {
		t.Fatalf("expected storage item for job %s, got %v", jobID, storageItems)
	}

	// 8. Verify DeleteJobBlob cleans up storage and metadata
	DeleteJobBlob(jobID, BlobKindAudioAAC)
	if HasJobBlob(jobID, BlobKindAudioAAC) {
		t.Fatalf("expected blob deleted")
	}
	ex, _ := memStorage.Exists(context.Background(), media.StorageKey)
	if ex {
		t.Fatalf("expected storage object deleted")
	}
}

func TestMigrateLegacyJobBlobs(t *testing.T) {
	tmpDir := t.TempDir()
	if err := InitWithFilename(tmpDir, "test_migration.db"); err != nil {
		t.Fatalf("init db: %v", err)
	}

	// Populate legacy job_blobs manually
	_, _ = dbConn.Exec(`INSERT INTO users(id, login_id, email, password_hash) VALUES ('user-mig', 'user-mig', 'mig@example.com', 'hash')`)
	jobID := "job-mig-1"
	job := &model.Job{OwnerID: "user-mig", Filename: "sample.pdf", FileType: "pdf", StatusCode: model.JobStatusCompletedCode}
	_ = SaveJob(jobID, job)

	pdfData := []byte("pdf-binary-legacy-data-98765")
	if err := SaveJobBinaryBlob(jobID, BlobKindPDFOriginal, pdfData); err != nil {
		t.Fatalf("save binary blob: %v", err)
	}

	// Setup storage for migration
	memStorage := storage.NewMemoryStorage()
	stats, err := MigrateJobBlobsToStorage(context.Background(), dbConn, memStorage, "memory", true, nil)
	if err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	if stats.MigratedBlobs != 1 || stats.MigratedBytes != int64(len(pdfData)) {
		t.Fatalf("stats mismatch: %+v", stats)
	}

	// Check that job_blobs was dropped
	if TableExists(dbConn, "job_blobs") {
		t.Fatalf("expected job_blobs table dropped")
	}

	// Register storage and verify LoadJobBlob
	SetBlobStorage(memStorage, "memory")
	defer SetBlobStorage(nil, "s3")

	loaded, err := LoadJobBlob(jobID, BlobKindPDFOriginal)
	if err != nil {
		t.Fatalf("load blob after migration: %v", err)
	}
	if string(loaded) != string(pdfData) {
		t.Fatalf("data mismatch after migration")
	}

	// Verify usage is still correctly reported from job_media
	usageMap, err := JobBlobUsageMapForJobIDs([]string{jobID})
	if err != nil {
		t.Fatalf("usage map: %v", err)
	}
	if usageMap[jobID] != int64(len(pdfData)) {
		t.Fatalf("expected usage %d, got %d", len(pdfData), usageMap[jobID])
	}
}
