package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"whisperserver/src/internal/integrations/storage"
	intutil "whisperserver/src/internal/util"
)

var (
	blobStorage     storage.Storage
	blobStorageName = "s3"
)

// SetBlobStorage registers the active object storage driver.
func SetBlobStorage(s storage.Storage, backendName string) {
	blobStorage = s
	if backendName != "" {
		blobStorageName = backendName
	}
}

// GetBlobStorage returns the active object storage driver.
func GetBlobStorage() storage.Storage {
	return blobStorage
}

// JobBlobUsage summarizes per-job blob storage usage.
type JobBlobUsage struct {
	JobID     string
	Bytes     int64
	BlobCount int
}

// SaveJobBlob upserts one named blob for a job.
func SaveJobBlob(jobID, kind string, data []byte) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	if isRuntimeArtifactKind(kind) {
		return SaveJobRuntimeArtifact(jobID, kind, data)
	}

	if blobStorage != nil {
		contentType := "application/octet-stream"
		switch kind {
		case BlobKindAudioAAC:
			contentType = "audio/mp4"
		case BlobKindPDFOriginal:
			contentType = "application/pdf"
		}
		key := fmt.Sprintf("jobs/%s/%s", jobID, kind)
		info, err := blobStorage.Put(context.Background(), key, data, contentType)
		if err != nil {
			return fmt.Errorf("storage put %q failed: %w", key, err)
		}
		if err := SaveJobMediaMetadata(jobID, kind, blobStorageName, key, info.Size, contentType, info.ETag); err != nil {
			_ = blobStorage.Delete(context.Background(), key)
			return fmt.Errorf("save job media metadata failed (rolled back storage object %q): %w", key, err)
		}
		return nil
	}

	return SaveJobBinaryBlob(jobID, kind, data)
}

// LoadJobBlob returns one named blob for a job.
func LoadJobBlob(jobID, kind string) ([]byte, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	if isRuntimeArtifactKind(kind) {
		return LoadJobRuntimeArtifact(jobID, kind)
	}

	// 1. Try to load from job_media + blobStorage
	media, err := GetJobMediaMetadata(jobID, kind)
	if err == nil && media != nil && blobStorage != nil {
		data, err := blobStorage.Get(context.Background(), media.StorageKey)
		if err == nil {
			return data, nil
		}
	}

	// 2. Fallback to legacy job_blobs table if present
	return LoadJobBinaryBlob(jobID, kind)
}

// HasJobBlob reports whether a named blob exists for a job.
func HasJobBlob(jobID, kind string) bool {
	if dbConn == nil {
		return false
	}
	if isRuntimeArtifactKind(kind) {
		return HasJobRuntimeArtifact(jobID, kind)
	}

	if HasJobMedia(jobID, kind) {
		return true
	}
	return HasJobBinaryBlob(jobID, kind)
}

// DeleteJobBlobs removes every blob attached to a job.
func DeleteJobBlobs(jobID string) {
	if dbConn == nil {
		return
	}
	DeleteAllJobRuntimeArtifacts(jobID)

	if blobStorage != nil {
		_ = blobStorage.DeletePrefix(context.Background(), fmt.Sprintf("jobs/%s/", jobID))
	}
	_ = DeleteAllJobMediaForJob(jobID)
	DeleteAllJobBinaryBlobs(jobID)
}

// DeleteJobBlob removes one named blob attached to a job.
func DeleteJobBlob(jobID, kind string) {
	if dbConn == nil {
		return
	}
	if isRuntimeArtifactKind(kind) {
		DeleteJobRuntimeArtifact(jobID, kind)
		return
	}

	if blobStorage != nil {
		key := fmt.Sprintf("jobs/%s/%s", jobID, kind)
		_ = blobStorage.Delete(context.Background(), key)
	}
	_ = DeleteJobMediaMetadata(jobID, kind)
	DeleteJobBinaryBlob(jobID, kind)
}

// ListJobBlobKinds lists every stored blob kind for a job.
func ListJobBlobKinds(jobID string) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	kinds, err := ListJobMediaKinds(jobID)
	if err != nil {
		kinds = []string{}
	}

	// Also check legacy job_blobs if table exists
	if TableExists(dbConn, "job_blobs") {
		rows, err := dbConn.Query(`SELECT kind FROM job_blobs WHERE job_id = ? ORDER BY kind`, jobID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var k string
				if err := rows.Scan(&k); err == nil {
					kinds = append(kinds, k)
				}
			}
		}
	}

	kinds = append(kinds, ListJobRuntimeArtifactKinds(jobID)...)
	out := intutil.UniqueStringsKeepOrder(kinds)
	sort.Strings(out)
	return out, nil
}

// ListJobBlobUsageByOwner aggregates blob storage usage for one owner.
func ListJobBlobUsageByOwner(ownerID string) ([]JobBlobUsage, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}

	hasLegacy := TableExists(dbConn, "job_blobs")
	var query string
	if hasLegacy {
		query = fmt.Sprintf(`
			SELECT id, size_bytes, blob_count
			FROM (
				SELECT
					j.id,
					j.filename,
					COALESCE((SELECT SUM(m.size_bytes) FROM job_media m WHERE m.job_id = j.id), 0) +
						COALESCE((SELECT SUM(LENGTH(b.data)) FROM job_blobs b WHERE b.job_id = j.id AND b.kind <> '%s'), 0) +
						COALESCE((SELECT SUM(LENGTH(r.data)) FROM job_json r WHERE r.job_id = j.id), 0) AS size_bytes,
					COALESCE((SELECT COUNT(1) FROM job_media m WHERE m.job_id = j.id), 0) +
						COALESCE((SELECT COUNT(1) FROM job_blobs b WHERE b.job_id = j.id AND b.kind <> '%s'), 0) +
						COALESCE((SELECT COUNT(1) FROM job_json r WHERE r.job_id = j.id), 0) AS blob_count
				FROM jobs j
				WHERE j.owner_id = ?
			)
			WHERE size_bytes > 0
			ORDER BY size_bytes DESC, filename COLLATE NOCASE ASC
		`, BlobKindWav, BlobKindWav)
	} else {
		query = `
			SELECT id, size_bytes, blob_count
			FROM (
				SELECT
					j.id,
					j.filename,
					COALESCE((SELECT SUM(m.size_bytes) FROM job_media m WHERE m.job_id = j.id), 0) +
						COALESCE((SELECT SUM(LENGTH(r.data)) FROM job_json r WHERE r.job_id = j.id), 0) AS size_bytes,
					COALESCE((SELECT COUNT(1) FROM job_media m WHERE m.job_id = j.id), 0) +
						COALESCE((SELECT COUNT(1) FROM job_json r WHERE r.job_id = j.id), 0) AS blob_count
				FROM jobs j
				WHERE j.owner_id = ?
			)
			WHERE size_bytes > 0
			ORDER BY size_bytes DESC, filename COLLATE NOCASE ASC
		`
	}

	rows, err := dbConn.Query(query, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []JobBlobUsage{}
	for rows.Next() {
		var item JobBlobUsage
		if err := rows.Scan(&item.JobID, &item.Bytes, &item.BlobCount); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// JobBlobUsageMapByOwner returns blob usage keyed by job ID.
func JobBlobUsageMapByOwner(ownerID string) (map[string]int64, error) {
	items, err := ListJobBlobUsageByOwner(ownerID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(items))
	for _, item := range items {
		out[item.JobID] = item.Bytes
	}
	return out, nil
}

// StorageJobItem aggregates storage usage and job metadata in a single query result.
type StorageJobItem struct {
	JobID       string
	Filename    string
	FileType    string
	FolderID    string
	IsTrashed   bool
	UploadedTS  float64
	StartedTS   float64
	CompletedTS float64
	SizeBytes   int64
	BlobCount   int
}

// ListStorageJobItemsByOwner returns jobs with storage usage and display metadata in a single bulk query.
func ListStorageJobItemsByOwner(ownerID string) ([]StorageJobItem, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}

	hasLegacy := TableExists(dbConn, "job_blobs")
	var query string
	if hasLegacy {
		query = fmt.Sprintf(`
			SELECT id, filename, file_type, folder_id, is_trashed, uploaded_ts, started_ts, completed_ts, size_bytes, blob_count
			FROM (
				SELECT
					j.id,
					j.filename,
					j.file_type,
					j.folder_id,
					j.is_trashed,
					j.uploaded_ts,
					j.started_ts,
					j.completed_ts,
					COALESCE((SELECT SUM(m.size_bytes) FROM job_media m WHERE m.job_id = j.id), 0) +
						COALESCE((SELECT SUM(LENGTH(b.data)) FROM job_blobs b WHERE b.job_id = j.id AND b.kind <> '%s'), 0) +
						COALESCE((SELECT SUM(LENGTH(r.data)) FROM job_json r WHERE r.job_id = j.id), 0) AS size_bytes,
					COALESCE((SELECT COUNT(1) FROM job_media m WHERE m.job_id = j.id), 0) +
						COALESCE((SELECT COUNT(1) FROM job_blobs b WHERE b.job_id = j.id AND b.kind <> '%s'), 0) +
						COALESCE((SELECT COUNT(1) FROM job_json r WHERE r.job_id = j.id), 0) AS blob_count
				FROM jobs j
				WHERE j.owner_id = ?
			)
			WHERE size_bytes > 0
			ORDER BY size_bytes DESC, filename COLLATE NOCASE ASC
		`, BlobKindWav, BlobKindWav)
	} else {
		query = `
			SELECT id, filename, file_type, folder_id, is_trashed, uploaded_ts, started_ts, completed_ts, size_bytes, blob_count
			FROM (
				SELECT
					j.id,
					j.filename,
					j.file_type,
					j.folder_id,
					j.is_trashed,
					j.uploaded_ts,
					j.started_ts,
					j.completed_ts,
					COALESCE((SELECT SUM(m.size_bytes) FROM job_media m WHERE m.job_id = j.id), 0) +
						COALESCE((SELECT SUM(LENGTH(r.data)) FROM job_json r WHERE r.job_id = j.id), 0) AS size_bytes,
					COALESCE((SELECT COUNT(1) FROM job_media m WHERE m.job_id = j.id), 0) +
						COALESCE((SELECT COUNT(1) FROM job_json r WHERE r.job_id = j.id), 0) AS blob_count
				FROM jobs j
				WHERE j.owner_id = ?
			)
			WHERE size_bytes > 0
			ORDER BY size_bytes DESC, filename COLLATE NOCASE ASC
		`
	}

	rows, err := dbConn.Query(query, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []StorageJobItem{}
	for rows.Next() {
		var (
			item        StorageJobItem
			folderIDCol sql.NullString
			isTrashed   int
		)
		if err := rows.Scan(
			&item.JobID,
			&item.Filename,
			&item.FileType,
			&folderIDCol,
			&isTrashed,
			&item.UploadedTS,
			&item.StartedTS,
			&item.CompletedTS,
			&item.SizeBytes,
			&item.BlobCount,
		); err != nil {
			return nil, err
		}
		item.FolderID = folderIDCol.String
		item.IsTrashed = isTrashed != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

// JobBlobUsageMapForJobIDs returns blob usage keyed by job ID for a specific slice of job IDs.
func JobBlobUsageMapForJobIDs(jobIDs []string) (map[string]int64, error) {
	if dbConn == nil || len(jobIDs) == 0 {
		return map[string]int64{}, nil
	}
	placeholders := make([]string, len(jobIDs))
	args := make([]any, 0, len(jobIDs)+1)
	for i, id := range jobIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	inClause := strings.Join(placeholders, ",")

	hasLegacy := TableExists(dbConn, "job_blobs")
	var query string
	if hasLegacy {
		query = fmt.Sprintf(`
			SELECT
				j.id,
				COALESCE((SELECT SUM(m.size_bytes) FROM job_media m WHERE m.job_id = j.id), 0) +
				COALESCE((SELECT SUM(LENGTH(b.data)) FROM job_blobs b WHERE b.job_id = j.id AND b.kind <> '%s'), 0) +
				COALESCE((SELECT SUM(LENGTH(r.data)) FROM job_json r WHERE r.job_id = j.id), 0) AS size_bytes
			FROM jobs j
			WHERE j.id IN (%s)
		`, BlobKindWav, inClause)
	} else {
		query = fmt.Sprintf(`
			SELECT
				j.id,
				COALESCE((SELECT SUM(m.size_bytes) FROM job_media m WHERE m.job_id = j.id), 0) +
				COALESCE((SELECT SUM(LENGTH(r.data)) FROM job_json r WHERE r.job_id = j.id), 0) AS size_bytes
			FROM jobs j
			WHERE j.id IN (%s)
		`, inClause)
	}

	rows, err := dbConn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64, len(jobIDs))
	for rows.Next() {
		var id string
		var size int64
		if err := rows.Scan(&id, &size); err != nil {
			return nil, err
		}
		out[id] = size
	}
	return out, rows.Err()
}

// SaveJobBinaryBlob stores durable binary payloads in SQLite (fallback or legacy).
func SaveJobBinaryBlob(jobID, kind string, data []byte) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	if !TableExists(dbConn, "job_blobs") {
		// If job_blobs was dropped, auto-recreate for tests or error
		_, err := dbConn.Exec(`CREATE TABLE IF NOT EXISTS job_blobs (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data BLOB NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind),
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)`)
		if err != nil {
			return err
		}
	}
	_, err := dbConn.Exec(`
		INSERT INTO job_blobs(job_id, kind, data, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(job_id, kind) DO UPDATE SET data=excluded.data, updated_at=CURRENT_TIMESTAMP
	`, jobID, kind, data)
	return err
}

// LoadJobBinaryBlob loads durable binary payloads from SQLite (fallback or legacy).
func LoadJobBinaryBlob(jobID, kind string) ([]byte, error) {
	if dbConn == nil || !TableExists(dbConn, "job_blobs") {
		return nil, fmt.Errorf("blob not found")
	}
	var b []byte
	err := dbConn.QueryRow(`SELECT data FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&b)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// HasJobBinaryBlob reports whether a durable SQLite blob exists.
func HasJobBinaryBlob(jobID, kind string) bool {
	if dbConn == nil || !TableExists(dbConn, "job_blobs") {
		return false
	}
	var n int
	err := dbConn.QueryRow(`SELECT COUNT(1) FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&n)
	if err != nil {
		return false
	}
	return n > 0
}

// DeleteAllJobBinaryBlobs removes every durable SQLite blob for one job.
func DeleteAllJobBinaryBlobs(jobID string) {
	if dbConn == nil || !TableExists(dbConn, "job_blobs") {
		return
	}
	_, _ = dbConn.Exec(`DELETE FROM job_blobs WHERE job_id = ?`, jobID)
}

// DeleteJobBinaryBlob removes one durable SQLite blob.
func DeleteJobBinaryBlob(jobID, kind string) {
	if dbConn == nil || !TableExists(dbConn, "job_blobs") {
		return
	}
	_, _ = dbConn.Exec(`DELETE FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind)
}
