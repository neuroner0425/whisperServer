package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// JobMedia represents the metadata stored in SQLite for an object storage media file.
type JobMedia struct {
	JobID          string
	Kind           string
	StorageBackend string
	StorageKey     string
	SizeBytes      int64
	ContentType    string
	ETag           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SaveJobMediaMetadata upserts metadata for an object stored in object storage.
func SaveJobMediaMetadata(jobID, kind, backend, key string, size int64, contentType, etag string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`
		INSERT INTO job_media(job_id, kind, storage_backend, storage_key, size_bytes, content_type, etag, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(job_id, kind) DO UPDATE SET
			storage_backend = excluded.storage_backend,
			storage_key     = excluded.storage_key,
			size_bytes      = excluded.size_bytes,
			content_type    = excluded.content_type,
			etag            = excluded.etag,
			updated_at      = CURRENT_TIMESTAMP
	`, jobID, kind, backend, key, size, contentType, etag)
	return err
}

// GetJobMediaMetadata loads metadata for a specific job and media kind.
func GetJobMediaMetadata(jobID, kind string) (*JobMedia, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	var m JobMedia
	var createdAtStr, updatedAtStr string
	err := dbConn.QueryRow(`
		SELECT job_id, kind, storage_backend, storage_key, size_bytes, content_type, etag, created_at, updated_at
		FROM job_media
		WHERE job_id = ? AND kind = ?
	`, jobID, kind).Scan(
		&m.JobID,
		&m.Kind,
		&m.StorageBackend,
		&m.StorageKey,
		&m.SizeBytes,
		&m.ContentType,
		&m.ETag,
		&createdAtStr,
		&updatedAtStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	m.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAtStr)
	m.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAtStr)
	return &m, nil
}

// HasJobMedia reports whether metadata for a media kind exists for the job.
func HasJobMedia(jobID, kind string) bool {
	if dbConn == nil {
		return false
	}
	var count int
	err := dbConn.QueryRow(`SELECT COUNT(1) FROM job_media WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// DeleteJobMediaMetadata deletes one media metadata record.
func DeleteJobMediaMetadata(jobID, kind string) error {
	if dbConn == nil {
		return nil
	}
	_, err := dbConn.Exec(`DELETE FROM job_media WHERE job_id = ? AND kind = ?`, jobID, kind)
	return err
}

// DeleteAllJobMediaForJob deletes all media metadata records for a job.
func DeleteAllJobMediaForJob(jobID string) error {
	if dbConn == nil {
		return nil
	}
	_, err := dbConn.Exec(`DELETE FROM job_media WHERE job_id = ?`, jobID)
	return err
}

// ListJobMediaKinds returns every media kind recorded for a job.
func ListJobMediaKinds(jobID string) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`SELECT kind FROM job_media WHERE job_id = ? ORDER BY kind`, jobID)
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
	return out, rows.Err()
}
