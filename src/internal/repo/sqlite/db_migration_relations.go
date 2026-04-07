package sqlite

import (
	"database/sql"
	"strings"
)

func ensureFoldersRelationalSchema(db *sql.DB) error {
	hasParentFK, err := foreignKeyReferencesTable(db, "folders", "folders")
	if err != nil {
		return err
	}
	if hasParentFK {
		return nil
	}
	_, err = db.Exec(`PRAGMA foreign_keys = OFF;`)
	if err != nil {
		return err
	}
	defer func() { _, _ = db.Exec(`PRAGMA foreign_keys = ON;`) }()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`ALTER TABLE folders RENAME TO folders_legacy`); err != nil {
		return err
	}
	if _, err = tx.Exec(`
		CREATE TABLE folders (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL,
			parent_id TEXT DEFAULT NULL,
			is_trashed INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (parent_id) REFERENCES folders(id) ON DELETE CASCADE
		)
	`); err != nil {
		return err
	}
	if _, err = tx.Exec(`
		INSERT INTO folders(id, owner_id, name, parent_id, is_trashed, updated_at)
		SELECT id, owner_id, name, NULLIF(TRIM(parent_id), ''), is_trashed, updated_at
		FROM folders_legacy
	`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DROP TABLE folders_legacy`); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureJobsRelationalSchema(db *sql.DB) error {
	hasOwnerFK, err := foreignKeyReferencesTable(db, "jobs", "users")
	if err != nil {
		return err
	}
	hasFolderFK, err := foreignKeyReferencesTable(db, "jobs", "folders")
	if err != nil {
		return err
	}
	if hasOwnerFK && hasFolderFK {
		return nil
	}
	_, err = db.Exec(`PRAGMA foreign_keys = OFF;`)
	if err != nil {
		return err
	}
	defer func() { _, _ = db.Exec(`PRAGMA foreign_keys = ON;`) }()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`ALTER TABLE jobs RENAME TO jobs_rel_legacy`); err != nil {
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
		INSERT INTO jobs(
			id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
			description, refine_enabled, owner_id, folder_id, is_trashed,
			deleted_ts, started_ts, completed_ts, progress_percent
		)
		SELECT
			id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
			description, refine_enabled, owner_id, NULLIF(TRIM(folder_id), ''), is_trashed,
			deleted_ts, started_ts, completed_ts, progress_percent
		FROM jobs_rel_legacy
	`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DROP TABLE jobs_rel_legacy`); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureTagsRelationalSchema(db *sql.DB) error {
	hasTagID, err := columnExists(db, "tags", "id")
	if err != nil {
		return err
	}
	hasJobTagID, err := columnExists(db, "job_tags", "tag_id")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			hasJobTagID = false
		} else {
			return err
		}
	}
	if hasTagID && hasJobTagID {
		return nil
	}
	_, err = db.Exec(`PRAGMA foreign_keys = OFF;`)
	if err != nil {
		return err
	}
	defer func() { _, _ = db.Exec(`PRAGMA foreign_keys = ON;`) }()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(`ALTER TABLE tags RENAME TO tags_legacy`); err != nil {
		return err
	}
	if _, err = tx.Exec(`
		CREATE TABLE tags (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (owner_id, name),
			FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE
		)
	`); err != nil {
		return err
	}
	if _, err = tx.Exec(`
		INSERT INTO tags(id, owner_id, name, description, updated_at)
		SELECT COALESCE(NULLIF(id, ''), lower(hex(randomblob(16)))), owner_id, name, description, updated_at
		FROM tags_legacy
	`); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such column: id") {
			if _, err = tx.Exec(`
				INSERT INTO tags(id, owner_id, name, description, updated_at)
				SELECT lower(hex(randomblob(16))), owner_id, name, description, updated_at
				FROM tags_legacy
			`); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	if _, err = tx.Exec(`DROP TABLE IF EXISTS job_tags_legacy`); err != nil {
		return err
	}
	if _, err = tx.Exec(`ALTER TABLE job_tags RENAME TO job_tags_legacy`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such table") {
		return err
	}
	if _, err = tx.Exec(`
		CREATE TABLE job_tags (
			job_id TEXT NOT NULL,
			tag_id TEXT NOT NULL,
			position INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, tag_id),
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE,
			FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
		)
	`); err != nil {
		return err
	}
	if _, err = tx.Exec(`
		INSERT INTO tags(id, owner_id, name, description, updated_at)
		SELECT lower(hex(randomblob(16))), j.owner_id, jt.tag_name, '', CURRENT_TIMESTAMP
		FROM job_tags_legacy jt
		JOIN jobs j ON j.id = jt.job_id
		LEFT JOIN tags t ON t.owner_id = j.owner_id AND t.name = jt.tag_name
		WHERE t.id IS NULL
	`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such table") {
		return err
	}
	if _, err = tx.Exec(`
		INSERT INTO job_tags(job_id, tag_id, position, updated_at)
		SELECT jt.job_id, t.id, jt.position, jt.updated_at
		FROM job_tags_legacy jt
		JOIN jobs j ON j.id = jt.job_id
		JOIN tags t ON t.owner_id = j.owner_id AND t.name = jt.tag_name
	`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such table") {
		return err
	}
	if _, err = tx.Exec(`DROP TABLE IF EXISTS job_tags_legacy`); err != nil {
		return err
	}
	if _, err = tx.Exec(`DROP TABLE tags_legacy`); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateRuntimeArtifactsToFilesystem(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT job_id, kind, data
		FROM job_blobs
		WHERE kind = ? OR kind = ? OR kind LIKE 'document_chunk_%'
	`, BlobKindPreview, BlobKindDocumentChunkIndex)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			jobID string
			kind  string
			data  []byte
		)
		if err := rows.Scan(&jobID, &kind, &data); err != nil {
			return err
		}
		if err := SaveJobRuntimeArtifact(jobID, kind, data); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`
		DELETE FROM job_blobs
		WHERE kind = ? OR kind = ? OR kind LIKE 'document_chunk_%'
	`, BlobKindPreview, BlobKindDocumentChunkIndex)
	return err
}
