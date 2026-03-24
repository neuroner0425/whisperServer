package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	BlobKindAudioAAC       = "audio_aac"
	BlobKindWav            = "wav"
	BlobKindPreview        = "preview"
	BlobKindTranscript     = "transcript"
	BlobKindTranscriptJSON = "transcript_json"
	BlobKindRefined        = "refined"
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
