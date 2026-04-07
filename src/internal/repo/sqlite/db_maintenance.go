package sqlite

import (
	"database/sql"
	"fmt"

	model "whisperserver/src/internal/domain"
)

func applyOneTimeMaintenance(db *sql.DB) error {
	version, err := currentDBMaintenanceVersion(db)
	if err != nil {
		return err
	}
	if version >= dbMaintenanceVersion {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	statements := []string{
		fmt.Sprintf(`
			UPDATE jobs
			SET status_code = %d
			WHERE file_type = 'pdf'
				AND status_code <> %d
				AND EXISTS (
					SELECT 1 FROM job_json j WHERE j.job_id = jobs.id AND j.kind = '%s'
				)
		`, model.JobStatusCompletedCode, model.JobStatusCompletedCode, BlobKindDocumentJSON),
		fmt.Sprintf(`
			UPDATE jobs
			SET status_code = %d
			WHERE file_type <> 'pdf'
				AND status_code <> %d
				AND EXISTS (
					SELECT 1 FROM job_json j WHERE j.job_id = jobs.id AND j.kind = '%s'
				)
		`, model.JobStatusCompletedCode, model.JobStatusCompletedCode, BlobKindTranscriptJSON),
		fmt.Sprintf(`
			UPDATE jobs
			SET status_code = %d
			WHERE status_code <> %d
				AND EXISTS (
					SELECT 1 FROM job_json j WHERE j.job_id = jobs.id AND j.kind = '%s'
				)
		`, model.JobStatusCompletedCode, model.JobStatusCompletedCode, BlobKindRefined),
		fmt.Sprintf(`DELETE FROM job_blobs WHERE kind IN ('%s', '%s')`, BlobKindTranscript, BlobKindDocumentMarkdown),
		fmt.Sprintf(`
			DELETE FROM job_blobs
			WHERE (kind = '%s' OR kind LIKE 'document_chunk_%%')
				AND EXISTS (
					SELECT 1 FROM job_json j WHERE j.job_id = job_blobs.job_id AND j.kind = '%s'
				)
		`, BlobKindDocumentChunkIndex, BlobKindDocumentJSON),
	}
	for _, stmt := range statements {
		if _, err = tx.Exec(stmt); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, dbMaintenanceVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func currentDBMaintenanceVersion(db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}
