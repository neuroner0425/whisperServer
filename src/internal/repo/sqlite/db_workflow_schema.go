package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
)

// ensureWorkflowSchema creates job_steps, tasks, outbox, and user_settings tables along with their indexes.
func ensureWorkflowSchema(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS job_steps (
			job_id         TEXT NOT NULL,
			step           TEXT NOT NULL,
			attempt        INTEGER NOT NULL DEFAULT 1,
			status         TEXT NOT NULL,
			input_hash     TEXT,
			artifact_key   TEXT,
			engine         TEXT,
			model          TEXT,
			prompt_sha     TEXT,
			params_json    TEXT,
			quality_report TEXT,
			error_message  TEXT,
			started_at     DATETIME NOT NULL,
			finished_at    DATETIME,
			PRIMARY KEY (job_id, step, attempt)
		);`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id             TEXT PRIMARY KEY,
			job_id         TEXT NOT NULL,
			owner_id       TEXT NOT NULL,
			step           TEXT NOT NULL,
			resource_class TEXT NOT NULL,
			priority       INTEGER NOT NULL DEFAULT 0,
			payload_json   TEXT,
			status         TEXT NOT NULL DEFAULT 'ready',
			lease_owner    TEXT,
			lease_until    DATETIME,
			attempt        INTEGER NOT NULL DEFAULT 0,
			max_attempts   INTEGER NOT NULL DEFAULT 5,
			run_after      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_claim ON tasks (status, resource_class, run_after, priority DESC, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_job_id ON tasks (job_id);`,
		`CREATE TABLE IF NOT EXISTS outbox (
			seq            INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_id       TEXT NOT NULL,
			job_id         TEXT,
			event_type     TEXT NOT NULL,
			payload_json   TEXT NOT NULL,
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			published_at   DATETIME
		);`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox (published_at) WHERE published_at IS NULL;`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_owner_seq ON outbox (owner_id, seq);`,
		`CREATE TABLE IF NOT EXISTS user_settings (
			user_id        TEXT PRIMARY KEY,
			llm_prefs_json TEXT NOT NULL,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("ensureWorkflowSchema exec failed: %w", err)
		}
	}

	// Expand jobs table with params_json and version if not present
	hasParams, err := columnExists(db, "jobs", "params_json")
	if err != nil {
		return err
	}
	if !hasParams {
		if _, err := db.Exec(`ALTER TABLE jobs ADD COLUMN params_json TEXT`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("add jobs.params_json failed: %w", err)
		}
	}

	hasVersion, err := columnExists(db, "jobs", "version")
	if err != nil {
		return err
	}
	if !hasVersion {
		if _, err := db.Exec(`ALTER TABLE jobs ADD COLUMN version INTEGER NOT NULL DEFAULT 1`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("add jobs.version failed: %w", err)
		}
	}

	return nil
}

