package store

import "database/sql"

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
