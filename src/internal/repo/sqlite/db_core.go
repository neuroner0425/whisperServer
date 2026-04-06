package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	model "whisperserver/src/internal/domain"

	_ "modernc.org/sqlite"
)

const (
	BlobKindAudioAAC           = "audio_aac"
	BlobKindWav                = "wav"
	BlobKindPDFOriginal        = "pdf_original"
	BlobKindPreview            = "preview"
	BlobKindTranscript         = "transcript"
	BlobKindTranscriptJSON     = "transcript_json"
	BlobKindRefined            = "refined"
	BlobKindDocumentJSON       = "document_json"
	BlobKindDocumentMarkdown   = "document_markdown"
	BlobKindDocumentChunkIndex = "document_chunk_index"
)

var (
	dbConn *sql.DB
	logf   = func(string, ...any) {}
	errorf = func(string, error, string, ...any) {}
)

func ConfigureLogging(info func(string, ...any), err func(string, error, string, ...any)) {
	if info != nil {
		logf = info
	}
	if err != nil {
		errorf = err
	}
}

func Init(projectRoot string) error {
	runDir := filepath.Join(projectRoot, ".run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}

	dbPath := filepath.Join(runDir, "whisper.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		return err
	}

	for _, s := range schemaStatements() {
		if _, err := db.Exec(s); err != nil {
			_ = db.Close()
			return err
		}
	}
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN login_id TEXT`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_login_id ON users(login_id)`); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_folders_owner_parent_trashed ON folders(owner_id, parent_id, is_trashed)`); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_folders_owner_parent_name ON folders(owner_id, parent_id, name)`); err != nil {
		_ = db.Close()
		return err
	}
	if err := ensureJobsSchema(db); err != nil {
		_ = db.Close()
		return err
	}

	dbConn = db
	logf("[DB] initialized path=%s", dbPath)
	return nil
}

func Close() {
	if dbConn != nil {
		_ = dbConn.Close()
		dbConn = nil
	}
}

func schemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS status_codes (
			code INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			login_id TEXT,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS jobs (
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
		);`,
		`CREATE TABLE IF NOT EXISTS job_blobs (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data BLOB NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind),
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS tags (
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (owner_id, name),
			FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS folders (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL,
			parent_id TEXT NOT NULL DEFAULT '',
			is_trashed INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
	}
}

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
