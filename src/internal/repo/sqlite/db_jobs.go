package sqlite

import (
	"database/sql"
	"fmt"

	model "whisperserver/src/internal/domain"
)

// LoadJobs hydrates the in-memory job snapshot from SQLite rows and blobs.
func LoadJobs() (map[string]*model.Job, error) {
	if dbConn == nil {
		return map[string]*model.Job{}, fmt.Errorf("db is not initialized")
	}

	rows, err := dbConn.Query(`
		SELECT
			id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
			description, refine_enabled, owner_id, folder_id, is_trashed,
			deleted_ts, started_ts, completed_ts, progress_percent
		FROM jobs
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobTags, err := loadAllJobTags(dbConn)
	if err != nil {
		return nil, err
	}

	out := make(map[string]*model.Job)
	for rows.Next() {
		var (
			id                   string
			statusCode           int
			filename             string
			fileType             string
			uploadedTS           float64
			mediaDurationSeconds sql.NullInt64
			description          string
			refineEnabled        int
			ownerID              string
			folderID             sql.NullString
			isTrashed            int
			deletedTS            float64
			startedTS            float64
			completedTS          float64
			progressPercent      int
		)
		if err := rows.Scan(
			&id, &statusCode, &filename, &fileType, &uploadedTS, &mediaDurationSeconds,
			&description, &refineEnabled, &ownerID, &folderID, &isTrashed,
			&deletedTS, &startedTS, &completedTS, &progressPercent,
		); err != nil {
			return nil, err
		}
		job := model.Job{
			StatusCode:      statusCode,
			Filename:        filename,
			FileType:        fileType,
			UploadedTS:      uploadedTS,
			Description:     description,
			RefineEnabled:   refineEnabled != 0,
			OwnerID:         ownerID,
			Tags:            jobTags[id],
			FolderID:        folderID.String,
			IsTrashed:       isTrashed != 0,
			DeletedTS:       deletedTS,
			StartedTS:       startedTS,
			CompletedTS:     completedTS,
			ProgressPercent: progressPercent,
		}
		if mediaDurationSeconds.Valid {
			v := int(mediaDurationSeconds.Int64)
			job.MediaDurationSeconds = &v
		}
		// Pull frequently needed blob-derived fields into the snapshot for fast runtime access.
		if preview, previewErr := LoadJobBlob(id, BlobKindPreview); previewErr == nil {
			job.PreviewText = string(preview)
		}
		if HasJobJSON(id, BlobKindTranscriptJSON) {
			job.Result = "db://transcript_json"
		}
		if HasJobJSON(id, BlobKindDocumentJSON) {
			job.Result = "db://document_json"
		}
		if HasJobJSON(id, BlobKindRefined) {
			job.ResultRefined = "db://refined"
		}
		out[id] = job.Clone()
	}
	return out, rows.Err()
}

// SaveJobs persists the entire in-memory snapshot back into SQLite.
func SaveJobs(snapshot map[string]*model.Job) (err error) {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}

	tx, err := dbConn.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	existing := map[string]struct{}{}
	rows, err := tx.Query(`SELECT id FROM jobs`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			_ = rows.Close()
			return scanErr
		}
		existing[id] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return err
	}

	// Upsert current jobs first, then remove rows no longer present in memory.
	tagIDsByOwner, err := loadTagIDsByOwner(tx)
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for id, job := range snapshot {
		if _, execErr := tx.Exec(`
			INSERT INTO jobs(
				id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
				description, refine_enabled, owner_id, folder_id, is_trashed,
				deleted_ts, started_ts, completed_ts, progress_percent
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				status_code=excluded.status_code,
				filename=excluded.filename,
				file_type=excluded.file_type,
				uploaded_ts=excluded.uploaded_ts,
				media_duration_seconds=excluded.media_duration_seconds,
				description=excluded.description,
				refine_enabled=excluded.refine_enabled,
				owner_id=excluded.owner_id,
				folder_id=excluded.folder_id,
				is_trashed=excluded.is_trashed,
				deleted_ts=excluded.deleted_ts,
				started_ts=excluded.started_ts,
				completed_ts=excluded.completed_ts,
				progress_percent=excluded.progress_percent
		`,
			id,
			job.StatusCode,
			job.Filename,
			job.FileType,
			job.UploadedTS,
			intOrNil(job.MediaDurationSeconds),
			job.Description,
			boolToInt(job.RefineEnabled),
			job.OwnerID,
			emptyStringAsNil(job.FolderID),
			boolToInt(job.IsTrashed),
			job.DeletedTS,
			job.StartedTS,
			job.CompletedTS,
			job.ProgressPercent,
		); execErr != nil {
			return execErr
		}
		seen[id] = struct{}{}
	}

	for id := range existing {
		if _, ok := seen[id]; ok {
			continue
		}
		if _, execErr := tx.Exec(`DELETE FROM jobs WHERE id = ?`, id); execErr != nil {
			return execErr
		}
	}

	if _, err := tx.Exec(`DELETE FROM job_tags`); err != nil {
		return err
	}
	insertTagStmt, err := tx.Prepare(`
		INSERT INTO job_tags(job_id, tag_id, position, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`)
	if err != nil {
		return err
	}
	defer insertTagStmt.Close()
	for jobID, job := range snapshot {
		ownerTags := tagIDsByOwner[job.OwnerID]
		for i, tag := range job.Tags {
			tagID := ownerTags[tag]
			if tagID == "" {
				continue
			}
			if _, err := insertTagStmt.Exec(jobID, tagID, i); err != nil {
				return err
			}
		}
	}

	err = tx.Commit()
	return err
}

func loadAllJobTags(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}) (map[string][]string, error) {
	rows, err := queryer.Query(`
		SELECT jt.job_id, t.name
		FROM job_tags jt
		JOIN tags t ON t.id = jt.tag_id
		ORDER BY jt.job_id, jt.position, t.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var jobID, tagName string
		if err := rows.Scan(&jobID, &tagName); err != nil {
			return nil, err
		}
		out[jobID] = append(out[jobID], tagName)
	}
	return out, rows.Err()
}

func loadTagIDsByOwner(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}) (map[string]map[string]string, error) {
	rows, err := queryer.Query(`SELECT owner_id, name, id FROM tags`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var ownerID, name, id string
		if err := rows.Scan(&ownerID, &name, &id); err != nil {
			return nil, err
		}
		if out[ownerID] == nil {
			out[ownerID] = map[string]string{}
		}
		out[ownerID][name] = id
	}
	return out, rows.Err()
}

func emptyStringAsNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
