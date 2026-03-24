package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"whisperserver/src/internal/model"
)

func columnExists(db *sql.DB, tableName, columnName string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, tableName))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return false, err
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, rows.Err()
}

func ensureStatusCodes(db *sql.DB) error {
	statements := []string{
		`INSERT INTO status_codes(code, name) VALUES (10, '작업 대기 중') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (20, '작업 중') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (30, '정제 대기 중') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (40, '정제 중') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (50, '완료') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (60, '실패') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

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
			tags_json TEXT NOT NULL DEFAULT '[]',
			folder_id TEXT NOT NULL DEFAULT '',
			is_trashed INTEGER NOT NULL DEFAULT 0,
			deleted_ts REAL NOT NULL DEFAULT 0,
			started_ts REAL NOT NULL DEFAULT 0,
			completed_ts REAL NOT NULL DEFAULT 0,
			progress_percent INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (status_code) REFERENCES status_codes(code)
		)
	`); err != nil {
		return err
	}

	if _, err = tx.Exec(`
		INSERT INTO jobs(
			id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
			description, refine_enabled, owner_id, tags_json, folder_id, is_trashed,
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
			CASE WHEN TRIM(tags_json) = '' THEN '[]' ELSE tags_json END,
			folder_id,
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

	if _, err = tx.Exec(`DROP TABLE jobs_legacy`); err != nil {
		return err
	}

	err = tx.Commit()
	return err
}

func encodeTagsJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeTagsJSON(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(value), &tags); err != nil {
		errorf("db.decodeTagsJSON", err, "value=%s", value)
		return nil
	}
	return tags
}

func intOrNil(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
