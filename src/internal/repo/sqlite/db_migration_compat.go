package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
)

// JSONCompressionReport summarizes compression state of job_json.
type JSONCompressionReport struct {
	TotalCount        int
	CompressedCount   int
	UncompressedCount int
	CompressedBytes   int64
	UncompressedBytes int64
}

// MediaHealthReport summarizes media presence in storage.
type MediaHealthReport struct {
	LegacyBlobsTableExists bool
	LegacyBlobsCount       int
	LegacyBlobsBytes       int64
	JobMediaCount          int
	JobMediaBytes          int64
	VerifiedPresentCount   int
	MissingObjectsCount    int
	MissingKeys            []string
}

// FTSHealthReport summarizes FTS5 index coverage.
type FTSHealthReport struct {
	TotalJobs          int
	IndexedJobs        int
	MissingIndexJobs   int
	NonEmptyIndexCount int
}

// OrphanReport summarizes orphaned and transient records.
type OrphanReport struct {
	OrphanJSONCount  int
	OrphanMediaCount int
	OrphanTagsCount  int
	OrphanFTSCount   int
	TransientKinds   int
}

// CheckJSONCompression inspects job_json for gzip compression.
func CheckJSONCompression(db *sql.DB) (JSONCompressionReport, error) {
	report := JSONCompressionReport{}
	if db == nil {
		return report, errors.New("db is nil")
	}

	rows, err := db.Query(`SELECT data FROM job_json`)
	if err != nil {
		return report, fmt.Errorf("query job_json: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return report, fmt.Errorf("scan job_json data: %w", err)
		}
		report.TotalCount++
		if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
			report.CompressedCount++
			report.CompressedBytes += int64(len(raw))
		} else {
			report.UncompressedCount++
			report.UncompressedBytes += int64(len(raw))
		}
	}
	return report, rows.Err()
}

// CompressUncompressedJobJSON compresses all non-gzip job_json rows in SQLite.
func CompressUncompressedJobJSON(
	db *sql.DB,
	onProgress func(done, total int, jobID, kind string, savedBytes int64),
) (totalMigrated int, totalSavedBytes int64, err error) {
	if db == nil {
		return 0, 0, errors.New("db is nil")
	}

	rows, err := db.Query(`SELECT job_id, kind, data FROM job_json`)
	if err != nil {
		return 0, 0, fmt.Errorf("query job_json: %w", err)
	}
	defer rows.Close()

	type uncompressedItem struct {
		jobID string
		kind  string
		data  []byte
	}
	var toCompress []uncompressedItem

	for rows.Next() {
		var item uncompressedItem
		if err := rows.Scan(&item.jobID, &item.kind, &item.data); err != nil {
			return 0, 0, fmt.Errorf("scan job_json: %w", err)
		}
		if len(item.data) < 2 || !(item.data[0] == 0x1f && item.data[1] == 0x8b) {
			toCompress = append(toCompress, item)
		}
	}
	_ = rows.Close()

	total := len(toCompress)
	if total == 0 {
		return 0, 0, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`UPDATE job_json SET data = ?, updated_at = CURRENT_TIMESTAMP WHERE job_id = ? AND kind = ?`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare update stmt: %w", err)
	}
	defer stmt.Close()

	for i, item := range toCompress {
		compressed, compErr := compressGzip(string(item.data))
		if compErr != nil {
			continue
		}
		if _, err := stmt.Exec(compressed, item.jobID, item.kind); err != nil {
			return totalMigrated, totalSavedBytes, fmt.Errorf("update job_json (%s, %s): %w", item.jobID, item.kind, err)
		}
		saved := int64(len(item.data)) - int64(len(compressed))
		totalSavedBytes += saved
		totalMigrated++
		if onProgress != nil {
			onProgress(i+1, total, item.jobID, item.kind, saved)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit compression tx: %w", err)
	}
	return totalMigrated, totalSavedBytes, nil
}

// CheckMediaStorageHealth checks job_blobs and job_media storage health.
func CheckMediaStorageHealth(ctx context.Context, db *sql.DB, s storage.Storage) (MediaHealthReport, error) {
	report := MediaHealthReport{}
	if db == nil {
		return report, errors.New("db is nil")
	}

	// 1. Check legacy job_blobs
	var dummy int
	err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='job_blobs'`).Scan(&dummy)
	if err == nil {
		report.LegacyBlobsTableExists = true
		_ = db.QueryRow(`SELECT COUNT(1), COALESCE(SUM(LENGTH(data)), 0) FROM job_blobs WHERE LENGTH(data) > 0`).
			Scan(&report.LegacyBlobsCount, &report.LegacyBlobsBytes)
	}

	// 2. Check job_media table
	err = db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='job_media'`).Scan(&dummy)
	if err == nil {
		rows, err := db.Query(`SELECT storage_key, size_bytes FROM job_media`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var key string
				var size int64
				if err := rows.Scan(&key, &size); err != nil {
					continue
				}
				report.JobMediaCount++
				report.JobMediaBytes += size

				if s != nil {
					reqCtx := ctx
					if reqCtx == nil {
						reqCtx = context.Background()
					}
					_, getErr := s.Get(reqCtx, key)
					if getErr != nil {
						report.MissingObjectsCount++
						if len(report.MissingKeys) < 10 {
							report.MissingKeys = append(report.MissingKeys, key)
						}
					} else {
						report.VerifiedPresentCount++
					}
				}
			}
		}
	}
	return report, nil
}

// CheckFTSHealth inspects job_search_fts index coverage.
func CheckFTSHealth(db *sql.DB) (FTSHealthReport, error) {
	report := FTSHealthReport{}
	if db == nil {
		return report, errors.New("db is nil")
	}

	_ = db.QueryRow(`SELECT COUNT(1) FROM jobs`).Scan(&report.TotalJobs)

	var dummy int
	err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='job_search_fts'`).Scan(&dummy)
	if err != nil {
		report.MissingIndexJobs = report.TotalJobs
		return report, nil
	}

	_ = db.QueryRow(`SELECT COUNT(1) FROM job_search_fts`).Scan(&report.IndexedJobs)
	_ = db.QueryRow(`SELECT COUNT(1) FROM job_search_fts WHERE LENGTH(content) > 0`).Scan(&report.NonEmptyIndexCount)
	_ = db.QueryRow(`SELECT COUNT(1) FROM jobs WHERE id NOT IN (SELECT job_id FROM job_search_fts)`).Scan(&report.MissingIndexJobs)

	return report, nil
}

// CheckOrphanRecords counts dangling records across related tables.
func CheckOrphanRecords(db *sql.DB) (OrphanReport, error) {
	report := OrphanReport{}
	if db == nil {
		return report, errors.New("db is nil")
	}

	_ = db.QueryRow(`SELECT COUNT(1) FROM job_json WHERE job_id NOT IN (SELECT id FROM jobs)`).Scan(&report.OrphanJSONCount)
	_ = db.QueryRow(`SELECT COUNT(1) FROM job_tags WHERE job_id NOT IN (SELECT id FROM jobs)`).Scan(&report.OrphanTagsCount)

	var dummy int
	if err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='job_media'`).Scan(&dummy); err == nil {
		_ = db.QueryRow(`SELECT COUNT(1) FROM job_media WHERE job_id NOT IN (SELECT id FROM jobs)`).Scan(&report.OrphanMediaCount)
	}
	if err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='job_search_fts'`).Scan(&dummy); err == nil {
		_ = db.QueryRow(`SELECT COUNT(1) FROM job_search_fts WHERE job_id NOT IN (SELECT id FROM jobs)`).Scan(&report.OrphanFTSCount)
	}
	_ = db.QueryRow(`SELECT COUNT(1) FROM job_json WHERE kind LIKE 'refine_diagnostics%' OR kind LIKE 'refine_%_candidate' OR kind = 'refine_invalid_cached_timeline'`).Scan(&report.TransientKinds)

	return report, nil
}

// CleanupOrphanAndTransientRecords removes orphaned dependent rows and old diagnostics.
func CleanupOrphanAndTransientRecords(db *sql.DB) (int64, error) {
	if db == nil {
		return 0, errors.New("db is nil")
	}

	queries := []string{
		`DELETE FROM job_json WHERE job_id NOT IN (SELECT id FROM jobs)`,
		`DELETE FROM job_tags WHERE job_id NOT IN (SELECT id FROM jobs)`,
		`DELETE FROM job_json WHERE kind LIKE 'refine_diagnostics%' OR kind LIKE 'refine_%_candidate' OR kind = 'refine_invalid_cached_timeline'`,
		`DELETE FROM job_json WHERE kind = 'refined_timeline' AND job_id IN (SELECT id FROM jobs WHERE status_code IN (50, 60))`,
	}

	var dummy int
	if err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='job_media'`).Scan(&dummy); err == nil {
		queries = append(queries, `DELETE FROM job_media WHERE job_id NOT IN (SELECT id FROM jobs)`)
	}
	if err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='job_search_fts'`).Scan(&dummy); err == nil {
		queries = append(queries, `DELETE FROM job_search_fts WHERE job_id NOT IN (SELECT id FROM jobs)`)
	}

	var totalDeleted int64
	for _, q := range queries {
		res, err := db.Exec(q)
		if err == nil {
			n, _ := res.RowsAffected()
			totalDeleted += n
		}
	}
	return totalDeleted, nil
}

// EnsureDatabaseIndexes ensures all optimized composite indexes exist.
func EnsureDatabaseIndexes(db *sql.DB) error {
	if db == nil {
		return errors.New("db is nil")
	}
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_jobs_status_active ON jobs(status_code) WHERE status_code IN (10, 20, 30, 40)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_owner_trashed_uploaded ON jobs(owner_id, is_trashed, uploaded_ts DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_owner_folder_trashed ON jobs(owner_id, folder_id, is_trashed)`,
		`CREATE INDEX IF NOT EXISTS idx_job_json_job_id ON job_json(job_id)`,
	}

	var dummy int
	if err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='job_media'`).Scan(&dummy); err == nil {
		stmts = append(stmts, `CREATE INDEX IF NOT EXISTS idx_job_media_job_id ON job_media(job_id)`)
	}
	hasTagID, _ := columnExists(db, "job_tags", "tag_id")
	if hasTagID {
		stmts = append(stmts, `CREATE INDEX IF NOT EXISTS idx_job_tags_tag_id ON job_tags(tag_id)`)
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec index (%s): %w", stmt, err)
		}
	}
	return nil
}

// CheckJobsStatusIntegrity verifies that all jobs in DB map to canonical status names.
func CheckJobsStatusIntegrity(db *sql.DB) (validCount, invalidCount int, invalidCodes []int, err error) {
	if db == nil {
		return 0, 0, nil, errors.New("db is nil")
	}
	rows, err := db.Query(`SELECT DISTINCT status_code FROM jobs`)
	if err != nil {
		return 0, 0, nil, err
	}
	defer rows.Close()

	seenCodes := make(map[int]bool)
	for rows.Next() {
		var code int
		if err := rows.Scan(&code); err != nil {
			return 0, 0, nil, err
		}
		seenCodes[code] = true
	}

	for code := range seenCodes {
		name := model.JobStatusName(code)
		if name == "" {
			invalidCount++
			invalidCodes = append(invalidCodes, code)
		} else {
			validCount++
		}
	}
	return validCount, invalidCount, invalidCodes, nil
}
