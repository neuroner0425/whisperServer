package store

import (
	"database/sql"
	"fmt"

	"whisperserver/src/internal/model"
)

func LoadJobs() (map[string]*model.Job, error) {
	if dbConn == nil {
		return map[string]*model.Job{}, fmt.Errorf("db is not initialized")
	}

	rows, err := dbConn.Query(`
		SELECT
			id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
			description, refine_enabled, owner_id, tags_json, folder_id, is_trashed,
			deleted_ts, started_ts, completed_ts, progress_percent
		FROM jobs
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
			tagsJSON             string
			folderID             string
			isTrashed            int
			deletedTS            float64
			startedTS            float64
			completedTS          float64
			progressPercent      int
		)
		if err := rows.Scan(
			&id, &statusCode, &filename, &fileType, &uploadedTS, &mediaDurationSeconds,
			&description, &refineEnabled, &ownerID, &tagsJSON, &folderID, &isTrashed,
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
			Tags:            decodeTagsJSON(tagsJSON),
			FolderID:        folderID,
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
		if preview, previewErr := LoadJobBlob(id, BlobKindPreview); previewErr == nil {
			job.PreviewText = string(preview)
		}
		if HasJobBlob(id, BlobKindTranscript) {
			job.Result = "db://transcript"
		}
		if HasJobBlob(id, BlobKindRefined) {
			job.ResultRefined = "db://refined"
		}
		out[id] = job.Clone()
	}
	return out, rows.Err()
}

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

	seen := map[string]struct{}{}
	for id, job := range snapshot {
		if _, execErr := tx.Exec(`
			INSERT INTO jobs(
				id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
				description, refine_enabled, owner_id, tags_json, folder_id, is_trashed,
				deleted_ts, started_ts, completed_ts, progress_percent
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				status_code=excluded.status_code,
				filename=excluded.filename,
				file_type=excluded.file_type,
				uploaded_ts=excluded.uploaded_ts,
				media_duration_seconds=excluded.media_duration_seconds,
				description=excluded.description,
				refine_enabled=excluded.refine_enabled,
				owner_id=excluded.owner_id,
				tags_json=excluded.tags_json,
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
			encodeTagsJSON(job.Tags),
			job.FolderID,
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

	err = tx.Commit()
	return err
}
