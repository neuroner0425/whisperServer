package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Init opens the SQLite database, applies pragmas, and ensures the schema is ready.
func Init(projectRoot string) error {
	runDir := filepath.Join(projectRoot, ".run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}

	dbPath := filepath.Join(runDir, "whisper.db")
	runtimeArtifactsDir = filepath.Join(runDir, "job_runtime")
	if err := os.MkdirAll(runtimeArtifactsDir, 0o755); err != nil {
		return err
	}
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
	for _, step := range initSteps() {
		if err := step(db); err != nil {
			_ = db.Close()
			return err
		}
	}

	dbConn = db
	logf("[DB] initialized path=%s", dbPath)
	return nil
}

func initSteps() []func(*sql.DB) error {
	return []func(*sql.DB) error{
		ensureBaseIndexes,
		ensureJobsSchema,
		ensureFoldersRelationalSchema,
		ensureJobsRelationalSchema,
		ensureTagsRelationalSchema,
		ensureBaseIndexes,
		ensureArtifactTables,
		repairLegacyJobForeignKeys,
		migrateRuntimeArtifactsToFilesystem,
		migrateLegacyJobJSONArtifacts,
		applyOneTimeMaintenance,
	}
}

func ensureBaseIndexes(db *sql.DB) error {
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_login_id ON users(login_id)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_owner_trashed_uploaded ON jobs(owner_id, is_trashed, uploaded_ts DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_owner_folder_trashed ON jobs(owner_id, folder_id, is_trashed)`,
		`CREATE INDEX IF NOT EXISTS idx_folders_owner_parent_trashed ON folders(owner_id, parent_id, is_trashed)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_folders_owner_parent_name ON folders(owner_id, parent_id, name)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureArtifactTables(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS job_tags (
			job_id TEXT NOT NULL,
			tag_id TEXT NOT NULL,
			position INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, tag_id),
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE,
			FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS job_json (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind),
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS job_blobs (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data BLOB NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind),
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
