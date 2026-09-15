package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"whisperserver/src/internal/integrations/storage"
)

// BlobMigrationStats records statistics of the blob migration run.
type BlobMigrationStats struct {
	TotalBlobs     int
	MigratedBlobs  int
	MigratedBytes  int64
	DroppedLegacy  bool
	VacuumExecuted bool
}

// MigrateJobBlobsToStorage migrates binary blobs from legacy job_blobs table to Storage and job_media.
// Once all rows are transferred, job_blobs is dropped and VACUUM is optionally executed.
func MigrateJobBlobsToStorage(
	ctx context.Context,
	db *sql.DB,
	s storage.Storage,
	backendName string,
	runVacuum bool,
	onProgress func(done, total int, jobID, kind string, bytes int64),
) (BlobMigrationStats, error) {
	stats := BlobMigrationStats{}
	if db == nil {
		return stats, errors.New("db is not initialized")
	}
	if s == nil {
		return stats, errors.New("storage is not initialized")
	}
	if backendName == "" {
		backendName = "s3"
	}

	// 1. Check if job_blobs table exists
	var dummy int
	err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='job_blobs'`).Scan(&dummy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return stats, nil
		}
		return stats, fmt.Errorf("check job_blobs table: %w", err)
	}

	// 2. Count non-empty blobs to migrate
	err = db.QueryRow(`SELECT COUNT(1) FROM job_blobs WHERE LENGTH(data) > 0`).Scan(&stats.TotalBlobs)
	if err != nil {
		return stats, fmt.Errorf("count job_blobs: %w", err)
	}

	if stats.TotalBlobs == 0 {
		// Nothing to migrate; drop table
		if _, err := db.Exec(`DROP TABLE IF EXISTS job_blobs`); err == nil {
			stats.DroppedLegacy = true
		}
		return stats, nil
	}

	// 3. Query all binary blobs
	rows, err := db.Query(`SELECT job_id, kind, data FROM job_blobs WHERE LENGTH(data) > 0`)
	if err != nil {
		return stats, fmt.Errorf("query job_blobs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if ctx != nil && ctx.Err() != nil {
			return stats, ctx.Err()
		}

		var (
			jobID string
			kind  string
			data  []byte
		)
		if err := rows.Scan(&jobID, &kind, &data); err != nil {
			return stats, fmt.Errorf("scan job_blob: %w", err)
		}

		// Don't migrate runtime artifacts that might have lingered in legacy table
		if isRuntimeArtifactKind(kind) {
			_ = SaveJobRuntimeArtifact(jobID, kind, data)
			_, _ = db.Exec(`DELETE FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind)
			continue
		}

		contentType := "application/octet-stream"
		switch kind {
		case BlobKindAudioAAC:
			contentType = "audio/mp4"
		case BlobKindPDFOriginal:
			contentType = "application/pdf"
		}

		key := fmt.Sprintf("jobs/%s/%s", jobID, kind)
		info, err := s.Put(ctx, key, data, contentType)
		if err != nil {
			return stats, fmt.Errorf("storage put %q failed: %w", key, err)
		}

		_, err = db.Exec(`
			INSERT INTO job_media(job_id, kind, storage_backend, storage_key, size_bytes, content_type, etag, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(job_id, kind) DO UPDATE SET
				storage_backend = excluded.storage_backend,
				storage_key     = excluded.storage_key,
				size_bytes      = excluded.size_bytes,
				content_type    = excluded.content_type,
				etag            = excluded.etag,
				updated_at      = CURRENT_TIMESTAMP
		`, jobID, kind, backendName, key, info.Size, contentType, info.ETag)
		if err != nil {
			return stats, fmt.Errorf("insert job_media for %s/%s failed: %w", jobID, kind, err)
		}

		_, _ = db.Exec(`DELETE FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind)

		stats.MigratedBlobs++
		stats.MigratedBytes += info.Size

		if onProgress != nil {
			onProgress(stats.MigratedBlobs, stats.TotalBlobs, jobID, kind, info.Size)
		}
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}

	// 4. Drop table after successful migration
	var remaining int
	_ = db.QueryRow(`SELECT COUNT(1) FROM job_blobs WHERE LENGTH(data) > 0`).Scan(&remaining)
	if remaining == 0 {
		if _, err := db.Exec(`DROP TABLE IF EXISTS job_blobs`); err == nil {
			stats.DroppedLegacy = true
		}
	}

	// 5. Reclaim disk space if requested
	if runVacuum && stats.DroppedLegacy {
		// VACUUM cannot be run inside a transaction, runs as raw exec
		if _, err := db.Exec(`VACUUM`); err == nil {
			stats.VacuumExecuted = true
		}
	}

	return stats, nil
}

// TableExists reports whether a table exists in SQLite.
func TableExists(db *sql.DB, tableName string) bool {
	if db == nil {
		return false
	}
	var dummy int
	err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, strings.TrimSpace(tableName)).Scan(&dummy)
	return err == nil
}
