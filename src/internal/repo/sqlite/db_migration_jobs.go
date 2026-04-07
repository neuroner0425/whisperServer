package sqlite

import (
	"database/sql"
	"encoding/json"

	model "whisperserver/src/internal/domain"
)

// ensureJobsSchema migrates older job tables into the current normalized shape.
func ensureJobsSchema(db *sql.DB) error {
	if err := ensureStatusCodes(db); err != nil {
		return err
	}

	normalized, err := jobsSchemaNormalized(db)
	if err != nil {
		return err
	}
	if normalized {
		return nil
	}

	jobColumns := []struct {
		name       string
		definition string
	}{
		{name: "status", definition: `ALTER TABLE jobs ADD COLUMN status TEXT NOT NULL DEFAULT ''`},
		{name: "filename", definition: `ALTER TABLE jobs ADD COLUMN filename TEXT NOT NULL DEFAULT ''`},
		{name: "file_type", definition: `ALTER TABLE jobs ADD COLUMN file_type TEXT NOT NULL DEFAULT ''`},
		{name: "uploaded_at", definition: `ALTER TABLE jobs ADD COLUMN uploaded_at TEXT NOT NULL DEFAULT ''`},
		{name: "uploaded_ts", definition: `ALTER TABLE jobs ADD COLUMN uploaded_ts REAL NOT NULL DEFAULT 0`},
		{name: "duration", definition: `ALTER TABLE jobs ADD COLUMN duration TEXT NOT NULL DEFAULT ''`},
		{name: "media_duration", definition: `ALTER TABLE jobs ADD COLUMN media_duration TEXT NOT NULL DEFAULT ''`},
		{name: "media_duration_seconds", definition: `ALTER TABLE jobs ADD COLUMN media_duration_seconds INTEGER`},
		{name: "description", definition: `ALTER TABLE jobs ADD COLUMN description TEXT NOT NULL DEFAULT ''`},
		{name: "refine_enabled", definition: `ALTER TABLE jobs ADD COLUMN refine_enabled INTEGER NOT NULL DEFAULT 0`},
		{name: "owner_id", definition: `ALTER TABLE jobs ADD COLUMN owner_id TEXT NOT NULL DEFAULT ''`},
		{name: "tags_json", definition: `ALTER TABLE jobs ADD COLUMN tags_json TEXT NOT NULL DEFAULT '[]'`},
		{name: "folder_id", definition: `ALTER TABLE jobs ADD COLUMN folder_id TEXT NOT NULL DEFAULT ''`},
		{name: "is_trashed", definition: `ALTER TABLE jobs ADD COLUMN is_trashed INTEGER NOT NULL DEFAULT 0`},
		{name: "deleted_at", definition: `ALTER TABLE jobs ADD COLUMN deleted_at TEXT NOT NULL DEFAULT ''`},
		{name: "started_at", definition: `ALTER TABLE jobs ADD COLUMN started_at TEXT NOT NULL DEFAULT ''`},
		{name: "started_ts", definition: `ALTER TABLE jobs ADD COLUMN started_ts REAL NOT NULL DEFAULT 0`},
		{name: "completed_at", definition: `ALTER TABLE jobs ADD COLUMN completed_at TEXT NOT NULL DEFAULT ''`},
		{name: "completed_ts", definition: `ALTER TABLE jobs ADD COLUMN completed_ts REAL NOT NULL DEFAULT 0`},
		{name: "progress_percent", definition: `ALTER TABLE jobs ADD COLUMN progress_percent INTEGER NOT NULL DEFAULT 0`},
		{name: "progress_label", definition: `ALTER TABLE jobs ADD COLUMN progress_label TEXT NOT NULL DEFAULT ''`},
	}

	for _, column := range jobColumns {
		exists, err := columnExists(db, "jobs", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := db.Exec(column.definition); err != nil {
			return err
		}
	}

	if err := migrateLegacyJobPayload(db); err != nil {
		return err
	}
	return normalizeJobsSchema(db)
}

// jobsSchemaNormalized reports whether the jobs table already matches the current schema.
func jobsSchemaNormalized(db *sql.DB) (bool, error) {
	hasStatusCode, err := columnExists(db, "jobs", "status_code")
	if err != nil {
		return false, err
	}
	if !hasStatusCode {
		return false, nil
	}
	legacyColumns := []string{
		"status",
		"uploaded_at",
		"duration",
		"media_duration",
		"tags_json",
		"deleted_at",
		"started_at",
		"completed_at",
		"progress_label",
		"payload",
		"updated_at",
		"phase",
		"status_detail",
	}
	for _, name := range legacyColumns {
		exists, err := columnExists(db, "jobs", name)
		if err != nil {
			return false, err
		}
		if exists {
			return false, nil
		}
	}
	return true, nil
}

// migrateLegacyJobPayload copies legacy payload columns into the normalized schema.
func migrateLegacyJobPayload(db *sql.DB) error {
	hasPayload, err := columnExists(db, "jobs", "payload")
	if err != nil {
		return err
	}
	if !hasPayload {
		return nil
	}

	rows, err := db.Query(`SELECT id, payload FROM jobs WHERE payload IS NOT NULL AND payload <> '' AND payload <> '{}'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for rows.Next() {
		var (
			id      string
			payload string
			job     model.Job
		)
		if err = rows.Scan(&id, &payload); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(payload), &job); err != nil {
			errorf("db.migrateLegacyJobPayload.unmarshal", err, "id=%s", id)
			continue
		}
		if _, err = tx.Exec(`
			UPDATE jobs
			SET status = ?,
				filename = ?,
				file_type = ?,
				uploaded_at = ?,
				uploaded_ts = ?,
				duration = ?,
				media_duration = ?,
				media_duration_seconds = ?,
				description = ?,
				refine_enabled = ?,
				owner_id = ?,
				tags_json = ?,
				folder_id = ?,
				is_trashed = ?,
				deleted_at = ?,
				started_at = ?,
				started_ts = ?,
				completed_at = ?,
				completed_ts = ?,
				phase = ?,
				progress_percent = ?,
				progress_label = ?,
				status_detail = ?,
				payload = '{}',
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`,
			job.Status,
			job.Filename,
			job.FileType,
			job.UploadedAt,
			job.UploadedTS,
			job.Duration,
			job.MediaDuration,
			intOrNil(job.MediaDurationSeconds),
			job.Description,
			boolToInt(job.RefineEnabled),
			job.OwnerID,
			encodeTagsJSON(job.Tags),
			job.FolderID,
			boolToInt(job.IsTrashed),
			job.DeletedAt,
			job.StartedAt,
			job.StartedTS,
			job.CompletedAt,
			job.CompletedTS,
			job.Phase,
			job.ProgressPercent,
			job.ProgressLabel,
			job.StatusDetail,
			id,
		); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	err = tx.Commit()
	return err
}

// normalizeJobsSchema rebuilds the jobs table into the current canonical layout.
func normalizeJobsSchema(db *sql.DB) error {
	normalized, err := jobsSchemaNormalized(db)
	if err != nil {
		return err
	}
	if normalized {
		return nil
	}

	if _, err := db.Exec(`PRAGMA foreign_keys = OFF;`); err != nil {
		return err
	}
	defer func() {
		_, _ = db.Exec(`PRAGMA foreign_keys = ON;`)
	}()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(`ALTER TABLE jobs RENAME TO jobs_legacy`); err != nil {
		return err
	}

	if _, err = tx.Exec(`
		CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			status_code INTEGER NOT NULL DEFAULT 10,
			filename TEXT NOT NULL DEFAULT '',
			file_type TEXT NOT NULL DEFAULT '',
			uploaded_ts REAL NOT NULL DEFAULT 0,
			media_duration_seconds INTEGER,
			description TEXT NOT NULL DEFAULT '',
			refine_enabled INTEGER NOT NULL DEFAULT 0,
			owner_id TEXT NOT NULL DEFAULT '',
			folder_id TEXT DEFAULT NULL,
			is_trashed INTEGER NOT NULL DEFAULT 0,
			deleted_ts REAL NOT NULL DEFAULT 0,
			started_ts REAL NOT NULL DEFAULT 0,
			completed_ts REAL NOT NULL DEFAULT 0,
			progress_percent INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (status_code) REFERENCES status_codes(code),
			FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (folder_id) REFERENCES folders(id) ON DELETE SET NULL
		)
	`); err != nil {
		return err
	}
	if _, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS job_tags (
			job_id TEXT NOT NULL,
			tag_name TEXT NOT NULL,
			position INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, tag_name),
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)
	`); err != nil {
		return err
	}

	if _, err = tx.Exec(`
		INSERT INTO jobs(
			id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
			description, refine_enabled, owner_id, folder_id, is_trashed,
			deleted_ts, started_ts, completed_ts, progress_percent
		)
		SELECT
			id,
			CASE TRIM(status)
				WHEN '작업 대기 중' THEN 10
				WHEN '작업 중' THEN 20
				WHEN '정제 대기 중' THEN 30
				WHEN '정제 중' THEN 40
				WHEN '완료' THEN 50
				WHEN '실패' THEN 60
				ELSE 10
			END,
			filename,
			file_type,
			CASE
				WHEN uploaded_ts > 0 THEN uploaded_ts
				WHEN TRIM(uploaded_at) <> '' THEN CAST(strftime('%s', uploaded_at) AS REAL)
				ELSE 0
			END,
			media_duration_seconds,
			description,
			refine_enabled,
			owner_id,
			NULLIF(TRIM(folder_id), ''),
			is_trashed,
			CASE
				WHEN TRIM(deleted_at) <> '' THEN CAST(strftime('%s', deleted_at) AS REAL)
				ELSE 0
			END,
			CASE
				WHEN started_ts > 0 THEN started_ts
				WHEN TRIM(started_at) <> '' THEN CAST(strftime('%s', started_at) AS REAL)
				ELSE 0
			END,
			CASE
				WHEN completed_ts > 0 THEN completed_ts
				WHEN TRIM(completed_at) <> '' THEN CAST(strftime('%s', completed_at) AS REAL)
				ELSE 0
			END,
			progress_percent
		FROM jobs_legacy
	`); err != nil {
		return err
	}

	if err = migrateLegacyJobTags(tx); err != nil {
		return err
	}

	if _, err = tx.Exec(`DROP TABLE jobs_legacy`); err != nil {
		return err
	}

	err = tx.Commit()
	return err
}

func migrateLegacyJobTags(tx *sql.Tx) error {
	hasTagsJSON, err := tableHasColumn(tx, "jobs_legacy", "tags_json")
	if err != nil || !hasTagsJSON {
		return err
	}

	rows, err := tx.Query(`SELECT id, tags_json FROM jobs_legacy WHERE TRIM(tags_json) <> '' AND tags_json <> '[]'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	insertStmt, err := tx.Prepare(`
		INSERT INTO job_tags(job_id, tag_name, position, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(job_id, tag_name) DO UPDATE SET
			position=excluded.position,
			updated_at=CURRENT_TIMESTAMP
	`)
	if err != nil {
		return err
	}
	defer insertStmt.Close()

	for rows.Next() {
		var (
			jobID    string
			tagsJSON string
		)
		if err := rows.Scan(&jobID, &tagsJSON); err != nil {
			return err
		}
		for i, tag := range decodeTagsJSON(tagsJSON) {
			if _, err := insertStmt.Exec(jobID, tag, i); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func migrateLegacyJobJSONArtifacts(db *sql.DB) error {
	kinds := []string{BlobKindTranscriptJSON, BlobKindRefined, BlobKindDocumentJSON}
	for _, kind := range kinds {
		rows, err := db.Query(`SELECT job_id, data FROM job_blobs WHERE kind = ?`, kind)
		if err != nil {
			return err
		}
		for rows.Next() {
			var (
				jobID string
				data  string
			)
			if err := rows.Scan(&jobID, &data); err != nil {
				_ = rows.Close()
				return err
			}
			if _, err := db.Exec(`
				INSERT INTO job_json(job_id, kind, data, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
				ON CONFLICT(job_id, kind) DO UPDATE SET data=excluded.data, updated_at=CURRENT_TIMESTAMP
			`, jobID, kind, data); err != nil {
				_ = rows.Close()
				return err
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if _, err := db.Exec(`DELETE FROM job_blobs WHERE kind = ?`, kind); err != nil {
			return err
		}
	}

	if _, err := db.Exec(`DELETE FROM job_blobs WHERE kind = ?`, BlobKindDocumentMarkdown); err != nil {
		return err
	}
	if _, err := db.Exec(`
		DELETE FROM job_blobs
		WHERE kind = ? AND EXISTS (
			SELECT 1 FROM job_json j WHERE j.job_id = job_blobs.job_id AND j.kind = ?
		)
	`, BlobKindTranscript, BlobKindTranscriptJSON); err != nil {
		return err
	}
	return nil
}
