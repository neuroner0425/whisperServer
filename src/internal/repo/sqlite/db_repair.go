package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
)

func repairLegacyJobForeignKeys(db *sql.DB) error {
	tables := []struct {
		name   string
		create string
		copy   string
	}{}

	hasJobTagID, err := columnExists(db, "job_tags", "tag_id")
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such table") {
		return err
	}
	if hasJobTagID {
		tables = append(tables, struct {
			name   string
			create string
			copy   string
		}{
			name: "job_tags",
			create: `CREATE TABLE job_tags_rebuild (
				job_id TEXT NOT NULL,
				tag_id TEXT NOT NULL,
				position INTEGER NOT NULL DEFAULT 0,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (job_id, tag_id),
				FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE,
				FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
			)`,
			copy: `INSERT INTO job_tags_rebuild(job_id, tag_id, position, updated_at)
				SELECT job_id, tag_id, position, updated_at FROM job_tags`,
		})
	} else {
		tables = append(tables, struct {
			name   string
			create string
			copy   string
		}{
			name: "job_tags",
			create: `CREATE TABLE job_tags_rebuild (
				job_id TEXT NOT NULL,
				tag_name TEXT NOT NULL,
				position INTEGER NOT NULL DEFAULT 0,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (job_id, tag_name),
				FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
			)`,
			copy: `INSERT INTO job_tags_rebuild(job_id, tag_name, position, updated_at)
				SELECT job_id, tag_name, position, updated_at FROM job_tags`,
		})
	}

	tables = append(tables, struct {
		name   string
		create string
		copy   string
	}{
		name: "job_blobs",
		create: `CREATE TABLE job_blobs_rebuild (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data BLOB NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind),
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)`,
		copy: `INSERT INTO job_blobs_rebuild(job_id, kind, data, updated_at)
			SELECT job_id, kind, data, updated_at FROM job_blobs`,
	})

	for _, table := range tables {
		needsRepair, err := foreignKeyReferencesTable(db, table.name, "jobs_legacy")
		if err != nil {
			return err
		}
		if !needsRepair {
			continue
		}
		if err := rebuildTable(db, table.name, table.create, table.copy); err != nil {
			return err
		}
	}
	return nil
}

func foreignKeyReferencesTable(db *sql.DB, tableName, target string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA foreign_key_list(%s)`, tableName))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id       int
			seq      int
			refTable string
			fromCol  string
			toCol    string
			onUpdate string
			onDelete string
			match    string
		)
		if err := rows.Scan(&id, &seq, &refTable, &fromCol, &toCol, &onUpdate, &onDelete, &match); err != nil {
			return false, err
		}
		if refTable == target {
			return true, nil
		}
	}
	return false, rows.Err()
}

func rebuildTable(db *sql.DB, tableName, createStmt, copyStmt string) (err error) {
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

	rebuildName := tableName + "_rebuild"
	if _, err = tx.Exec(`DROP TABLE IF EXISTS ` + rebuildName); err != nil {
		return err
	}
	if _, err = tx.Exec(createStmt); err != nil {
		return err
	}
	if _, err = tx.Exec(copyStmt); err != nil {
		return err
	}
	if _, err = tx.Exec(`DROP TABLE ` + tableName); err != nil {
		return err
	}
	if _, err = tx.Exec(`ALTER TABLE ` + rebuildName + ` RENAME TO ` + tableName); err != nil {
		return err
	}
	return tx.Commit()
}
