package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	model "whisperserver/src/internal/domain"
)

// LoadJobs hydrates the in-memory job snapshot from SQLite rows and blobs.
func LoadJobs() (map[string]*model.Job, error) {
	if dbConn == nil {
		return map[string]*model.Job{}, fmt.Errorf("db is not initialized")
	}

	jobTags, err := loadAllJobTags(dbConn)
	if err != nil {
		return nil, err
	}

	jobJSONKinds, err := loadAllJobJSONKinds(dbConn)
	if err != nil {
		return nil, err
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
			Status:          model.JobStatusName(statusCode),
			Phase:           model.JobPhase(statusCode),
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
		if kinds := jobJSONKinds[id]; kinds != nil {
			if kinds[BlobKindTranscriptJSON] {
				job.Result = "db://transcript_json"
			}
			if kinds[BlobKindDocumentJSON] {
				job.Result = "db://document_json"
			}
			if kinds[BlobKindRefined] {
				job.ResultRefined = "db://refined"
			}
		}
		out[id] = job.Clone()
	}
	return out, rows.Err()
}

// SaveJob persists or updates a single job record and synchronizes its tags in SQLite.
func SaveJob(id string, job *model.Job) (err error) {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	if job == nil {
		return nil
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

	// Synchronize full-text search owner_id
	_, _ = tx.Exec(`UPDATE job_search_fts SET owner_id = ? WHERE job_id = ?`, job.OwnerID, id)

	// Synchronize tags only for this specific job.
	if _, err := tx.Exec(`DELETE FROM job_tags WHERE job_id = ?`, id); err != nil {
		return err
	}
	if len(job.Tags) > 0 && job.OwnerID != "" {
		insertTagStmt, err := tx.Prepare(`
			INSERT INTO job_tags(job_id, tag_id, position, updated_at)
			SELECT ?, id, ?, CURRENT_TIMESTAMP
			FROM tags
			WHERE owner_id = ? AND name = ?
		`)
		if err != nil {
			return err
		}
		defer insertTagStmt.Close()

		for i, tag := range job.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if _, err := insertTagStmt.Exec(id, i, job.OwnerID, tag); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// DeleteJobs removes multiple job rows and relies on CASCADE foreign keys to clean up related records.
func DeleteJobs(ids []string) (err error) {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	if len(ids) == 0 {
		return nil
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

	// 1. Explicitly delete dependent tables to guarantee integrity even if FK cascade is off
	childTables := []string{"job_json", "job_tags", "job_media", "job_blobs", "job_search_fts"}
	for _, tbl := range childTables {
		delStmt, prepErr := tx.Prepare(fmt.Sprintf(`DELETE FROM %s WHERE job_id = ?`, tbl))
		if prepErr != nil {
			continue
		}
		for _, id := range ids {
			_, _ = delStmt.Exec(id)
		}
		_ = delStmt.Close()
	}

	// 2. Delete parent jobs
	stmt, err := tx.Prepare(`DELETE FROM jobs WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, execErr := stmt.Exec(id); execErr != nil {
			return execErr
		}
	}

	return tx.Commit()
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

func loadAllJobJSONKinds(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}) (map[string]map[string]bool, error) {
	rows, err := queryer.Query(`SELECT job_id, kind FROM job_json`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]map[string]bool{}
	for rows.Next() {
		var jobID, kind string
		if err := rows.Scan(&jobID, &kind); err != nil {
			return nil, err
		}
		if out[jobID] == nil {
			out[jobID] = map[string]bool{}
		}
		out[jobID][kind] = true
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

// JobQueryFilter contains parameters for database-level file listing queries.
type JobQueryFilter struct {
	OwnerID      string
	FolderID     string
	FilterFolder bool
	IsTrashed    bool
	SearchQuery  string
	Tag          string
	SortBy       string
	SortOrder    string
	Page         int
	PageSize     int
}

// JobRecord pairs a job ID with its job domain model.
type JobRecord struct {
	ID  string
	Job *model.Job
}

// PagedJobsResult contains paginated jobs along with total counts.
type PagedJobsResult struct {
	Records    []JobRecord
	TotalItems int
	TotalPages int
	Page       int
	PageSize   int
}

// QueryJobsPaged executes an indexed, paginated query directly against SQLite.
func QueryJobsPaged(filter JobQueryFilter) (*PagedJobsResult, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}

	whereClauses := []string{"owner_id = ?", "is_trashed = ?"}
	whereArgs := []any{filter.OwnerID, boolToInt(filter.IsTrashed)}

	if filter.FilterFolder {
		folderID := strings.TrimSpace(filter.FolderID)
		if folderID == "" {
			whereClauses = append(whereClauses, "(folder_id IS NULL OR folder_id = '')")
		} else {
			whereClauses = append(whereClauses, "folder_id = ?")
			whereArgs = append(whereArgs, folderID)
		}
	}

	searchQuery := strings.TrimSpace(filter.SearchQuery)
	if searchQuery != "" {
		ftsMatch := sanitizeFTS5Query(searchQuery)
		if ftsMatch != "" {
			if filter.OwnerID != "" {
				whereClauses = append(whereClauses, "(LOWER(filename) LIKE ? OR id IN (SELECT job_id FROM job_search_fts WHERE owner_id = ? AND job_search_fts MATCH ?))")
				whereArgs = append(whereArgs, "%"+strings.ToLower(searchQuery)+"%", filter.OwnerID, ftsMatch)
			} else {
				whereClauses = append(whereClauses, "(LOWER(filename) LIKE ? OR id IN (SELECT job_id FROM job_search_fts WHERE job_search_fts MATCH ?))")
				whereArgs = append(whereArgs, "%"+strings.ToLower(searchQuery)+"%", ftsMatch)
			}
		} else {
			whereClauses = append(whereClauses, "LOWER(filename) LIKE ?")
			whereArgs = append(whereArgs, "%"+strings.ToLower(searchQuery)+"%")
		}
	}

	tag := strings.TrimSpace(filter.Tag)
	if tag != "" {
		whereClauses = append(whereClauses, "id IN (SELECT jt.job_id FROM job_tags jt JOIN tags t ON jt.tag_id = t.id WHERE t.owner_id = ? AND t.name = ?)")
		whereArgs = append(whereArgs, filter.OwnerID, tag)
	}

	whereSQL := "WHERE " + strings.Join(whereClauses, " AND ")

	// 1. Total count
	countQuery := "SELECT COUNT(1) FROM jobs " + whereSQL
	var totalItems int
	if err := dbConn.QueryRow(countQuery, whereArgs...).Scan(&totalItems); err != nil {
		return nil, err
	}

	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	totalPages := (totalItems + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	page := filter.Page
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * pageSize

	// 2. Order by
	orderSQL := "ORDER BY uploaded_ts DESC"
	if filter.SortBy == "name" {
		orderDir := "ASC"
		if strings.ToLower(filter.SortOrder) == "desc" {
			orderDir = "DESC"
		}
		orderSQL = fmt.Sprintf("ORDER BY LOWER(filename) %s, uploaded_ts DESC", orderDir)
	} else if strings.ToLower(filter.SortOrder) == "asc" {
		orderSQL = "ORDER BY uploaded_ts ASC"
	}

	// 3. Query page
	selectSQL := fmt.Sprintf(`
		SELECT
			id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
			description, refine_enabled, owner_id, folder_id, is_trashed,
			deleted_ts, started_ts, completed_ts, progress_percent
		FROM jobs
		%s
		%s
		LIMIT ? OFFSET ?
	`, whereSQL, orderSQL)

	pageArgs := append([]any(nil), whereArgs...)
	pageArgs = append(pageArgs, pageSize, offset)

	rows, err := dbConn.Query(selectSQL, pageArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]JobRecord, 0, pageSize)
	jobIDs := make([]string, 0, pageSize)

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
		job := &model.Job{
			StatusCode:      statusCode,
			Status:          model.JobStatusName(statusCode),
			Filename:        filename,
			FileType:        fileType,
			UploadedTS:      uploadedTS,
			Description:     description,
			RefineEnabled:   refineEnabled != 0,
			OwnerID:         ownerID,
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
		records = append(records, JobRecord{ID: id, Job: job})
		jobIDs = append(jobIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 4. Batch load tags and JSON kinds for these jobIDs
	if len(jobIDs) > 0 {
		tagsMap, err := loadTagsForJobIDs(dbConn, jobIDs)
		if err != nil {
			return nil, err
		}
		kindsMap, err := loadJSONKindsForJobIDs(dbConn, jobIDs)
		if err != nil {
			return nil, err
		}
		for i := range records {
			id := jobIDs[i]
			records[i].Job.Tags = tagsMap[id]
			if kinds := kindsMap[id]; kinds != nil {
				if kinds[BlobKindTranscriptJSON] {
					records[i].Job.Result = "db://transcript_json"
				}
				if kinds[BlobKindDocumentJSON] {
					records[i].Job.Result = "db://document_json"
				}
				if kinds[BlobKindRefined] {
					records[i].Job.Result = "db://refined"
				}
			}
		}
	}

	return &PagedJobsResult{
		Records:    records,
		TotalItems: totalItems,
		TotalPages: totalPages,
		Page:       page,
		PageSize:   pageSize,
	}, nil
}

func chunkSlice[T any](slice []T, chunkSize int) [][]T {
	if len(slice) == 0 {
		return nil
	}
	if chunkSize <= 0 {
		return [][]T{slice}
	}
	var chunks [][]T
	for i := 0; i < len(slice); i += chunkSize {
		end := i + chunkSize
		if end > len(slice) {
			end = len(slice)
		}
		chunks = append(chunks, slice[i:end])
	}
	return chunks
}

func loadTagsForJobIDs(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}, jobIDs []string) (map[string][]string, error) {
	if len(jobIDs) == 0 {
		return map[string][]string{}, nil
	}
	out := make(map[string][]string, len(jobIDs))
	for _, chunk := range chunkSlice(jobIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		query := fmt.Sprintf(`
			SELECT jt.job_id, t.name
			FROM job_tags jt
			JOIN tags t ON jt.tag_id = t.id
			WHERE jt.job_id IN (%s)
			ORDER BY jt.position ASC
		`, strings.Join(placeholders, ","))

		rows, err := queryer.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var jobID, tagName string
			if err := rows.Scan(&jobID, &tagName); err != nil {
				rows.Close()
				return nil, err
			}
			out[jobID] = append(out[jobID], tagName)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func loadJSONKindsForJobIDs(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}, jobIDs []string) (map[string]map[string]bool, error) {
	if len(jobIDs) == 0 {
		return map[string]map[string]bool{}, nil
	}
	out := make(map[string]map[string]bool, len(jobIDs))
	for _, chunk := range chunkSlice(jobIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		query := fmt.Sprintf(`SELECT job_id, kind FROM job_json WHERE job_id IN (%s)`, strings.Join(placeholders, ","))
		rows, err := queryer.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var jobID, kind string
			if err := rows.Scan(&jobID, &kind); err != nil {
				rows.Close()
				return nil, err
			}
			if out[jobID] == nil {
				out[jobID] = map[string]bool{}
			}
			out[jobID][kind] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// GetJobByID retrieves a single job and its associated tags, JSON blobs, and preview from SQLite.
func GetJobByID(id string) (*model.Job, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}

	var (
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

	err := dbConn.QueryRow(`
		SELECT
			status_code, filename, file_type, uploaded_ts, media_duration_seconds,
			description, refine_enabled, owner_id, folder_id, is_trashed,
			deleted_ts, started_ts, completed_ts, progress_percent
		FROM jobs
		WHERE id = ?
	`, id).Scan(
		&statusCode, &filename, &fileType, &uploadedTS, &mediaDurationSeconds,
		&description, &refineEnabled, &ownerID, &folderID, &isTrashed,
		&deletedTS, &startedTS, &completedTS, &progressPercent,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	tags, _ := loadTagsForJobIDs(dbConn, []string{id})
	jsonKinds, _ := loadJSONKindsForJobIDs(dbConn, []string{id})

	job := model.Job{
		StatusCode:      statusCode,
		Status:          model.JobStatusName(statusCode),
		Phase:           model.JobPhase(statusCode),
		Filename:        filename,
		FileType:        fileType,
		UploadedTS:      uploadedTS,
		Description:     description,
		RefineEnabled:   refineEnabled != 0,
		OwnerID:         ownerID,
		Tags:            tags[id],
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
	if preview, previewErr := LoadJobBlob(id, BlobKindPreview); previewErr == nil {
		job.PreviewText = string(preview)
	}
	if kinds := jsonKinds[id]; kinds != nil {
		if kinds[BlobKindTranscriptJSON] {
			job.Result = "db://transcript_json"
		}
		if kinds[BlobKindDocumentJSON] {
			job.Result = "db://document_json"
		}
		if kinds[BlobKindRefined] {
			job.ResultRefined = "db://refined"
		}
	}
	return job.Clone(), nil
}

// LoadActiveJobs hydrates only active (pending/running/refining) jobs into memory.
func LoadActiveJobs() (map[string]*model.Job, error) {
	if dbConn == nil {
		return map[string]*model.Job{}, fmt.Errorf("db is not initialized")
	}

	jobTags, err := loadAllJobTags(dbConn)
	if err != nil {
		return nil, err
	}

	jobJSONKinds, err := loadAllJobJSONKinds(dbConn)
	if err != nil {
		return nil, err
	}

	rows, err := dbConn.Query(`
		SELECT
			id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
			description, refine_enabled, owner_id, folder_id, is_trashed,
			deleted_ts, started_ts, completed_ts, progress_percent
		FROM jobs
		WHERE status_code IN (10, 20, 30, 40)
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
		if preview, previewErr := LoadJobBlob(id, BlobKindPreview); previewErr == nil {
			job.PreviewText = string(preview)
		}
		if kinds := jobJSONKinds[id]; kinds != nil {
			if kinds[BlobKindTranscriptJSON] {
				job.Result = "db://transcript_json"
			}
			if kinds[BlobKindDocumentJSON] {
				job.Result = "db://document_json"
			}
			if kinds[BlobKindRefined] {
				job.ResultRefined = "db://refined"
			}
		}
		out[id] = job.Clone()
	}
	return out, rows.Err()
}

// ListJobIDsByFolderIDs returns all job IDs belonging to the given folders for an owner.
func ListJobIDsByFolderIDs(ownerID string, folderIDs []string) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	if len(folderIDs) == 0 {
		return nil, nil
	}
	var allIDs []string
	for _, chunk := range chunkSlice(folderIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)+1)
		args = append(args, ownerID)
		for i, fid := range chunk {
			placeholders[i] = "?"
			args = append(args, fid)
		}

		query := fmt.Sprintf(`SELECT id FROM jobs WHERE owner_id = ? AND folder_id IN (%s)`, strings.Join(placeholders, ","))
		rows, err := dbConn.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			allIDs = append(allIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return allIDs, nil
}

// ListCompletedJobsByFolderIDs returns all completed, non-trashed jobs in the given folders.
func ListCompletedJobsByFolderIDs(ownerID string, folderIDs []string) ([]JobRecord, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	if len(folderIDs) == 0 {
		return nil, nil
	}
	var records []JobRecord
	var jobIDs []string
	for _, chunk := range chunkSlice(folderIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)+1)
		args = append(args, ownerID)
		for i, fid := range chunk {
			placeholders[i] = "?"
			args = append(args, fid)
		}

		query := fmt.Sprintf(`
			SELECT
				id, status_code, filename, file_type, uploaded_ts, media_duration_seconds,
				description, refine_enabled, owner_id, folder_id, is_trashed,
				deleted_ts, started_ts, completed_ts, progress_percent
			FROM jobs
			WHERE owner_id = ? AND status_code = 50 AND is_trashed = 0 AND folder_id IN (%s)
		`, strings.Join(placeholders, ","))

		rows, err := dbConn.Query(query, args...)
		if err != nil {
			return nil, err
		}
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
				ownerIDCol           string
				folderIDCol          sql.NullString
				isTrashed            int
				deletedTS            float64
				startedTS            float64
				completedTS          float64
				progressPercent      int
			)
			if err := rows.Scan(
				&id, &statusCode, &filename, &fileType, &uploadedTS, &mediaDurationSeconds,
				&description, &refineEnabled, &ownerIDCol, &folderIDCol, &isTrashed,
				&deletedTS, &startedTS, &completedTS, &progressPercent,
			); err != nil {
				rows.Close()
				return nil, err
			}
			job := &model.Job{
				StatusCode:      statusCode,
				Filename:        filename,
				FileType:        fileType,
				UploadedTS:      uploadedTS,
				Description:     description,
				RefineEnabled:   refineEnabled != 0,
				OwnerID:         ownerIDCol,
				FolderID:        folderIDCol.String,
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
			records = append(records, JobRecord{ID: id, Job: job})
			jobIDs = append(jobIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	kindsMap, _ := loadJSONKindsForJobIDs(dbConn, jobIDs)
	for _, rec := range records {
		if kinds := kindsMap[rec.ID]; kinds != nil {
			if kinds[BlobKindTranscriptJSON] {
				rec.Job.Result = "db://transcript_json"
			}
			if kinds[BlobKindDocumentJSON] {
				rec.Job.Result = "db://document_json"
			}
			if kinds[BlobKindRefined] {
				rec.Job.ResultRefined = "db://refined"
			}
		}
	}
	return records, nil
}

// ListTrashedJobIDs returns all trashed job IDs belonging to the given owner.
func ListTrashedJobIDs(ownerID string) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`SELECT id FROM jobs WHERE owner_id = ? AND is_trashed = 1`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkJobsTrashedByFolderIDs sets is_trashed = 1 and deleted_ts for all jobs belonging to the given folders.
func MarkJobsTrashedByFolderIDs(ownerID string, folderIDs []string, deletedTS float64) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	if len(folderIDs) == 0 {
		return nil
	}
	for _, chunk := range chunkSlice(folderIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)+2)
		args = append(args, deletedTS, ownerID)
		for i, fid := range chunk {
			placeholders[i] = "?"
			args = append(args, fid)
		}

		query := fmt.Sprintf(`UPDATE jobs SET is_trashed = 1, deleted_ts = ? WHERE owner_id = ? AND folder_id IN (%s)`, strings.Join(placeholders, ","))
		if _, err := dbConn.Exec(query, args...); err != nil {
			return err
		}
	}
	return nil
}

// GetOwnerIDsByJobIDs returns distinct owner IDs for the given job IDs.
func GetOwnerIDsByJobIDs(jobIDs []string) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	if len(jobIDs) == 0 {
		return nil, nil
	}
	ownerSet := make(map[string]struct{})
	for _, chunk := range chunkSlice(jobIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		query := fmt.Sprintf(`SELECT DISTINCT owner_id FROM jobs WHERE id IN (%s) AND owner_id <> ''`, strings.Join(placeholders, ","))
		rows, err := dbConn.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ownerID string
			if err := rows.Scan(&ownerID); err == nil && ownerID != "" {
				ownerSet[ownerID] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	owners := make([]string, 0, len(ownerSet))
	for o := range ownerSet {
		owners = append(owners, o)
	}
	return owners, nil
}

// FilterJobIDsByOwner returns the subset of jobIDs owned by ownerID, optionally filtering by trash state.
func FilterJobIDsByOwner(ownerID string, jobIDs []string, trashedOnly bool) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	if len(jobIDs) == 0 {
		return nil, nil
	}
	var matchedIDs []string
	for _, chunk := range chunkSlice(jobIDs, 500) {
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)+1)
		args = append(args, ownerID)
		for i, id := range chunk {
			placeholders[i] = "?"
			args = append(args, id)
		}
		extraWhere := ""
		if trashedOnly {
			extraWhere = " AND is_trashed = 1"
		}
		query := fmt.Sprintf(`SELECT id FROM jobs WHERE owner_id = ? AND id IN (%s)%s`, strings.Join(placeholders, ","), extraWhere)
		rows, err := dbConn.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			matchedIDs = append(matchedIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return matchedIDs, nil
}
