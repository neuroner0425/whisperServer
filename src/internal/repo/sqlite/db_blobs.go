package sqlite

import (
	"fmt"
	"sort"
)

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
	return HasJobBinaryBlob(jobID, kind)
}

// DeleteJobBlobs removes every blob attached to a job.
func DeleteJobBlobs(jobID string) {
	if dbConn == nil {
		return
	}
	DeleteAllJobBinaryBlobs(jobID)
	DeleteAllJobRuntimeArtifacts(jobID)
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
	DeleteJobBinaryBlob(jobID, kind)
}

// ListJobBlobKinds lists every stored blob kind for a job.
func ListJobBlobKinds(jobID string) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`SELECT kind FROM job_blobs WHERE job_id = ? ORDER BY kind`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, err
		}
		out = append(out, kind)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out = append(out, ListJobRuntimeArtifactKinds(jobID)...)
	sort.Strings(out)
	return out, nil
}

// ListJobBlobUsageByOwner aggregates blob storage usage for one owner.
func ListJobBlobUsageByOwner(ownerID string) ([]JobBlobUsage, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`
		SELECT id, size_bytes, blob_count
		FROM (
			SELECT
				j.id,
				j.filename,
				COALESCE((SELECT SUM(LENGTH(b.data)) FROM job_blobs b WHERE b.job_id = j.id AND b.kind <> ?), 0) +
					COALESCE((SELECT SUM(LENGTH(r.data)) FROM job_json r WHERE r.job_id = j.id), 0) AS size_bytes,
				COALESCE((SELECT COUNT(1) FROM job_blobs b WHERE b.job_id = j.id AND b.kind <> ?), 0) +
					COALESCE((SELECT COUNT(1) FROM job_json r WHERE r.job_id = j.id), 0) AS blob_count
			FROM jobs j
			WHERE j.owner_id = ?
		)
		WHERE size_bytes > 0
		ORDER BY size_bytes DESC, filename COLLATE NOCASE ASC
	`, BlobKindWav, BlobKindWav, ownerID)
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

// SaveJobBinaryBlob stores durable binary payloads in SQLite.
func SaveJobBinaryBlob(jobID, kind string, data []byte) error {
	_, err := dbConn.Exec(`
		INSERT INTO job_blobs(job_id, kind, data, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(job_id, kind) DO UPDATE SET data=excluded.data, updated_at=CURRENT_TIMESTAMP
	`, jobID, kind, data)
	return err
}

// LoadJobBinaryBlob loads durable binary payloads from SQLite.
func LoadJobBinaryBlob(jobID, kind string) ([]byte, error) {
	var b []byte
	err := dbConn.QueryRow(`SELECT data FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&b)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// HasJobBinaryBlob reports whether a durable SQLite blob exists.
func HasJobBinaryBlob(jobID, kind string) bool {
	var n int
	err := dbConn.QueryRow(`SELECT COUNT(1) FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&n)
	if err != nil {
		return false
	}
	return n > 0
}

// DeleteAllJobBinaryBlobs removes every durable SQLite blob for one job.
func DeleteAllJobBinaryBlobs(jobID string) {
	_, _ = dbConn.Exec(`DELETE FROM job_blobs WHERE job_id = ?`, jobID)
}

// DeleteJobBinaryBlob removes one durable SQLite blob.
func DeleteJobBinaryBlob(jobID, kind string) {
	_, _ = dbConn.Exec(`DELETE FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind)
}
