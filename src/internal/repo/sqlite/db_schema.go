package sqlite

// schemaStatements returns the base schema created for a fresh database.
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
			folder_id TEXT DEFAULT NULL,
			is_trashed INTEGER NOT NULL DEFAULT 0,
			deleted_ts REAL NOT NULL DEFAULT 0,
			started_ts REAL NOT NULL DEFAULT 0,
			completed_ts REAL NOT NULL DEFAULT 0,
			progress_percent INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (status_code) REFERENCES status_codes(code),
			FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (folder_id) REFERENCES folders(id) ON DELETE SET NULL
		);`,
		`CREATE TABLE IF NOT EXISTS tags (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (owner_id, name),
			FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS folders (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL,
			parent_id TEXT DEFAULT NULL,
			is_trashed INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (parent_id) REFERENCES folders(id) ON DELETE CASCADE
		);`,
	}
}
