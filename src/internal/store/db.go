package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
	"whisperserver/src/internal/model"
)

const (
	BlobKindWav        = "wav"
	BlobKindPreview    = "preview"
	BlobKindTranscript = "transcript"
	BlobKindRefined    = "refined"
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

func schemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			login_id TEXT,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL DEFAULT '',
			file_type TEXT NOT NULL DEFAULT '',
			uploaded_at TEXT NOT NULL DEFAULT '',
			uploaded_ts REAL NOT NULL DEFAULT 0,
			duration TEXT NOT NULL DEFAULT '',
			media_duration TEXT NOT NULL DEFAULT '',
			media_duration_seconds INTEGER,
			description TEXT NOT NULL DEFAULT '',
			refine_enabled INTEGER NOT NULL DEFAULT 0,
			owner_id TEXT NOT NULL DEFAULT '',
			tags_json TEXT NOT NULL DEFAULT '[]',
			folder_id TEXT NOT NULL DEFAULT '',
			is_trashed INTEGER NOT NULL DEFAULT 0,
			deleted_at TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL DEFAULT '',
			started_ts REAL NOT NULL DEFAULT 0,
			completed_at TEXT NOT NULL DEFAULT '',
			completed_ts REAL NOT NULL DEFAULT 0,
			phase TEXT NOT NULL DEFAULT '',
			progress_percent INTEGER NOT NULL DEFAULT 0,
			progress_label TEXT NOT NULL DEFAULT '',
			status_detail TEXT NOT NULL DEFAULT '',
			payload TEXT NOT NULL DEFAULT '{}',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS job_blobs (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data BLOB NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind),
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS tags (
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (owner_id, name),
			FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS folders (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL,
			parent_id TEXT NOT NULL DEFAULT '',
			is_trashed INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
	}
}

func ensureJobsSchema(db *sql.DB) error {
	jobColumns := []struct {
		name       string
		definition string
	}{
		{name: "status", definition: `ALTER TABLE jobs ADD COLUMN status TEXT NOT NULL DEFAULT ''`},
		{name: "filename", definition: `ALTER TABLE jobs ADD COLUMN filename TEXT NOT NULL DEFAULT ''`},
		{name: "file_type", definition: `ALTER TABLE jobs ADD COLUMN file_type TEXT NOT NULL DEFAULT ''`},
		{name: "uploaded_at", definition: `ALTER TABLE jobs ADD COLUMN uploaded_at TEXT NOT NULL DEFAULT ''`},
		{name: "uploaded_ts", definition: `ALTER TABLE jobs ADD COLUMN uploaded_ts REAL NOT NULL DEFAULT 0`},
		{name: "duration", definition: `ALTER TABLE jobs ADD COLUMN duration TEXT NOT NULL DEFAULT ''`},
		{name: "media_duration", definition: `ALTER TABLE jobs ADD COLUMN media_duration TEXT NOT NULL DEFAULT ''`},
		{name: "media_duration_seconds", definition: `ALTER TABLE jobs ADD COLUMN media_duration_seconds INTEGER`},
		{name: "description", definition: `ALTER TABLE jobs ADD COLUMN description TEXT NOT NULL DEFAULT ''`},
		{name: "refine_enabled", definition: `ALTER TABLE jobs ADD COLUMN refine_enabled INTEGER NOT NULL DEFAULT 0`},
		{name: "owner_id", definition: `ALTER TABLE jobs ADD COLUMN owner_id TEXT NOT NULL DEFAULT ''`},
		{name: "tags_json", definition: `ALTER TABLE jobs ADD COLUMN tags_json TEXT NOT NULL DEFAULT '[]'`},
		{name: "folder_id", definition: `ALTER TABLE jobs ADD COLUMN folder_id TEXT NOT NULL DEFAULT ''`},
		{name: "is_trashed", definition: `ALTER TABLE jobs ADD COLUMN is_trashed INTEGER NOT NULL DEFAULT 0`},
		{name: "deleted_at", definition: `ALTER TABLE jobs ADD COLUMN deleted_at TEXT NOT NULL DEFAULT ''`},
		{name: "started_at", definition: `ALTER TABLE jobs ADD COLUMN started_at TEXT NOT NULL DEFAULT ''`},
		{name: "started_ts", definition: `ALTER TABLE jobs ADD COLUMN started_ts REAL NOT NULL DEFAULT 0`},
		{name: "completed_at", definition: `ALTER TABLE jobs ADD COLUMN completed_at TEXT NOT NULL DEFAULT ''`},
		{name: "completed_ts", definition: `ALTER TABLE jobs ADD COLUMN completed_ts REAL NOT NULL DEFAULT 0`},
		{name: "phase", definition: `ALTER TABLE jobs ADD COLUMN phase TEXT NOT NULL DEFAULT ''`},
		{name: "progress_percent", definition: `ALTER TABLE jobs ADD COLUMN progress_percent INTEGER NOT NULL DEFAULT 0`},
		{name: "progress_label", definition: `ALTER TABLE jobs ADD COLUMN progress_label TEXT NOT NULL DEFAULT ''`},
		{name: "status_detail", definition: `ALTER TABLE jobs ADD COLUMN status_detail TEXT NOT NULL DEFAULT ''`},
	}

	for _, column := range jobColumns {
		exists, err := columnExists(db, "jobs", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := db.Exec(column.definition); err != nil {
			return err
		}
	}

	return migrateLegacyJobPayload(db)
}

func columnExists(db *sql.DB, tableName, columnName string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, tableName))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return false, err
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, rows.Err()
}

func migrateLegacyJobPayload(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, payload FROM jobs WHERE payload IS NOT NULL AND payload <> '' AND payload <> '{}'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for rows.Next() {
		var (
			id      string
			payload string
			job     model.Job
		)
		if err = rows.Scan(&id, &payload); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(payload), &job); err != nil {
			errorf("db.migrateLegacyJobPayload.unmarshal", err, "id=%s", id)
			continue
		}
		if _, err = tx.Exec(`
			UPDATE jobs
			SET status = ?,
				filename = ?,
				file_type = ?,
				uploaded_at = ?,
				uploaded_ts = ?,
				duration = ?,
				media_duration = ?,
				media_duration_seconds = ?,
				description = ?,
				refine_enabled = ?,
				owner_id = ?,
				tags_json = ?,
				folder_id = ?,
				is_trashed = ?,
				deleted_at = ?,
				started_at = ?,
				started_ts = ?,
				completed_at = ?,
				completed_ts = ?,
				phase = ?,
				progress_percent = ?,
				progress_label = ?,
				status_detail = ?,
				payload = '{}',
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`,
			job.Status,
			job.Filename,
			job.FileType,
			job.UploadedAt,
			job.UploadedTS,
			job.Duration,
			job.MediaDuration,
			intOrNil(job.MediaDurationSeconds),
			job.Description,
			boolToInt(job.RefineEnabled),
			job.OwnerID,
			encodeTagsJSON(job.Tags),
			job.FolderID,
			boolToInt(job.IsTrashed),
			job.DeletedAt,
			job.StartedAt,
			job.StartedTS,
			job.CompletedAt,
			job.CompletedTS,
			job.Phase,
			job.ProgressPercent,
			job.ProgressLabel,
			job.StatusDetail,
			id,
		); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	err = tx.Commit()
	return err
}

func encodeTagsJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeTagsJSON(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(value), &tags); err != nil {
		errorf("db.decodeTagsJSON", err, "value=%s", value)
		return nil
	}
	return tags
}

func intOrNil(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func LoadJobs() (map[string]*model.Job, error) {
	if dbConn == nil {
		return map[string]*model.Job{}, fmt.Errorf("db is not initialized")
	}

	rows, err := dbConn.Query(`
		SELECT
			id,
			status,
			filename,
			file_type,
			uploaded_at,
			uploaded_ts,
			duration,
			media_duration,
			media_duration_seconds,
			description,
			refine_enabled,
			owner_id,
			tags_json,
			folder_id,
			is_trashed,
			deleted_at,
			started_at,
			started_ts,
			completed_at,
			completed_ts,
			phase,
			progress_percent,
			progress_label,
			status_detail
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
			status               string
			filename             string
			fileType             string
			uploadedAt           string
			uploadedTS           float64
			duration             string
			mediaDuration        string
			mediaDurationSeconds sql.NullInt64
			description          string
			refineEnabled        int
			ownerID              string
			tagsJSON             string
			folderID             string
			isTrashed            int
			deletedAt            string
			startedAt            string
			startedTS            float64
			completedAt          string
			completedTS          float64
			phase                string
			progressPercent      int
			progressLabel        string
			statusDetail         string
		)
		if err := rows.Scan(
			&id,
			&status,
			&filename,
			&fileType,
			&uploadedAt,
			&uploadedTS,
			&duration,
			&mediaDuration,
			&mediaDurationSeconds,
			&description,
			&refineEnabled,
			&ownerID,
			&tagsJSON,
			&folderID,
			&isTrashed,
			&deletedAt,
			&startedAt,
			&startedTS,
			&completedAt,
			&completedTS,
			&phase,
			&progressPercent,
			&progressLabel,
			&statusDetail,
		); err != nil {
			return nil, err
		}
		job := model.Job{
			Status:          status,
			Filename:        filename,
			FileType:        fileType,
			UploadedAt:      uploadedAt,
			UploadedTS:      uploadedTS,
			Duration:        duration,
			MediaDuration:   mediaDuration,
			Description:     description,
			RefineEnabled:   refineEnabled != 0,
			OwnerID:         ownerID,
			Tags:            decodeTagsJSON(tagsJSON),
			FolderID:        folderID,
			IsTrashed:       isTrashed != 0,
			DeletedAt:       deletedAt,
			StartedAt:       startedAt,
			StartedTS:       startedTS,
			CompletedAt:     completedAt,
			CompletedTS:     completedTS,
			Phase:           phase,
			ProgressPercent: progressPercent,
			ProgressLabel:   progressLabel,
			StatusDetail:    statusDetail,
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
				id, status, filename, file_type, uploaded_at, uploaded_ts, duration,
				media_duration, media_duration_seconds, description, refine_enabled, owner_id,
				tags_json, folder_id, is_trashed, deleted_at, started_at, started_ts,
				completed_at, completed_ts, phase, progress_percent, progress_label,
				status_detail, payload, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '{}', CURRENT_TIMESTAMP)
			ON CONFLICT(id) DO UPDATE SET
				status=excluded.status,
				filename=excluded.filename,
				file_type=excluded.file_type,
				uploaded_at=excluded.uploaded_at,
				uploaded_ts=excluded.uploaded_ts,
				duration=excluded.duration,
				media_duration=excluded.media_duration,
				media_duration_seconds=excluded.media_duration_seconds,
				description=excluded.description,
				refine_enabled=excluded.refine_enabled,
				owner_id=excluded.owner_id,
				tags_json=excluded.tags_json,
				folder_id=excluded.folder_id,
				is_trashed=excluded.is_trashed,
				deleted_at=excluded.deleted_at,
				started_at=excluded.started_at,
				started_ts=excluded.started_ts,
				completed_at=excluded.completed_at,
				completed_ts=excluded.completed_ts,
				phase=excluded.phase,
				progress_percent=excluded.progress_percent,
				progress_label=excluded.progress_label,
				status_detail=excluded.status_detail,
				payload='{}',
				updated_at=CURRENT_TIMESTAMP
		`,
			id,
			job.Status,
			job.Filename,
			job.FileType,
			job.UploadedAt,
			job.UploadedTS,
			job.Duration,
			job.MediaDuration,
			intOrNil(job.MediaDurationSeconds),
			job.Description,
			boolToInt(job.RefineEnabled),
			job.OwnerID,
			encodeTagsJSON(job.Tags),
			job.FolderID,
			boolToInt(job.IsTrashed),
			job.DeletedAt,
			job.StartedAt,
			job.StartedTS,
			job.CompletedAt,
			job.CompletedTS,
			job.Phase,
			job.ProgressPercent,
			job.ProgressLabel,
			job.StatusDetail,
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

func SaveJobBlob(jobID, kind string, data []byte) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`
		INSERT INTO job_blobs(job_id, kind, data, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(job_id, kind) DO UPDATE SET data=excluded.data, updated_at=CURRENT_TIMESTAMP
	`, jobID, kind, data)
	return err
}

func LoadJobBlob(jobID, kind string) ([]byte, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	var b []byte
	err := dbConn.QueryRow(`SELECT data FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&b)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func HasJobBlob(jobID, kind string) bool {
	if dbConn == nil {
		return false
	}
	var n int
	err := dbConn.QueryRow(`SELECT COUNT(1) FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&n)
	if err != nil {
		return false
	}
	return n > 0
}

func DeleteJobBlobs(jobID string) {
	if dbConn == nil {
		return
	}
	_, _ = dbConn.Exec(`DELETE FROM job_blobs WHERE job_id = ?`, jobID)
}

func DeleteJobBlob(jobID, kind string) {
	if dbConn == nil {
		return
	}
	_, _ = dbConn.Exec(`DELETE FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind)
}

func CreateUser(loginID, email, passwordHash string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	id := uuid.NewString()
	_, err := dbConn.Exec(`INSERT INTO users(id, login_id, email, password_hash) VALUES (?, ?, ?, ?)`, id, loginID, email, passwordHash)
	return err
}

func FindUserByIdentifier(identifier string) (*model.UserRecord, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	var u model.UserRecord
	err := dbConn.QueryRow(`SELECT id, login_id, email, password_hash FROM users WHERE lower(email) = lower(?) OR login_id = ?`, identifier, identifier).
		Scan(&u.ID, &u.LoginID, &u.Email, &u.PasswordHash)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func UpsertTag(ownerID, name, description string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`
		INSERT INTO tags(owner_id, name, description, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(owner_id, name) DO UPDATE SET description=excluded.description, updated_at=CURRENT_TIMESTAMP
	`, ownerID, name, description)
	return err
}

func ListTagsByOwner(ownerID string) ([]model.Tag, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`SELECT name, description FROM tags WHERE owner_id = ? ORDER BY name`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.Tag{}
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.Name, &t.Description); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func ListTagNamesByOwner(ownerID string) (map[string]struct{}, error) {
	tags, err := ListTagsByOwner(ownerID)
	if err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	for _, t := range tags {
		out[t.Name] = struct{}{}
	}
	return out, nil
}

func GetTagDescriptionsByNames(ownerID string, names []string) (map[string]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	out := map[string]string{}
	if len(names) == 0 {
		return out, nil
	}

	stmt := `SELECT name, description FROM tags WHERE owner_id = ? AND name = ?`
	for _, n := range names {
		var name, desc string
		if err := dbConn.QueryRow(stmt, ownerID, n).Scan(&name, &desc); err != nil {
			continue
		}
		out[name] = desc
	}
	return out, nil
}

func DeleteTag(ownerID, name string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`DELETE FROM tags WHERE owner_id = ? AND name = ?`, ownerID, name)
	return err
}

func DeleteFoldersByOwner(ownerID string, folderIDs []string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	if len(folderIDs) == 0 {
		return nil
	}
	for _, id := range folderIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, err := dbConn.Exec(`DELETE FROM folders WHERE owner_id = ? AND id = ?`, ownerID, id); err != nil {
			return err
		}
	}
	return nil
}

func DeleteTrashedFoldersByOwner(ownerID string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`DELETE FROM folders WHERE owner_id = ? AND is_trashed = 1`, ownerID)
	return err
}

func CreateFolder(ownerID, name, parentID string) (string, error) {
	if dbConn == nil {
		return "", fmt.Errorf("db is not initialized")
	}
	id := uuid.NewString()
	if strings.TrimSpace(parentID) == "" {
		parentID = ""
	}
	_, err := dbConn.Exec(`
		INSERT INTO folders(id, owner_id, name, parent_id, is_trashed, updated_at)
		VALUES (?, ?, ?, ?, 0, CURRENT_TIMESTAMP)
	`, id, ownerID, name, parentID)
	return id, err
}

func ListFoldersByParent(ownerID, parentID string, trashed bool) ([]model.Folder, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	if strings.TrimSpace(parentID) == "" {
		parentID = ""
	}
	rows, err := dbConn.Query(`
		SELECT id, owner_id, name, parent_id, is_trashed, updated_at
		FROM folders
		WHERE owner_id = ? AND parent_id = ? AND is_trashed = ?
		ORDER BY name
	`, ownerID, parentID, boolToInt(trashed))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.Folder{}
	for rows.Next() {
		var f model.Folder
		var trashedInt int
		if err := rows.Scan(&f.ID, &f.OwnerID, &f.Name, &f.ParentID, &trashedInt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.IsTrashed = trashedInt != 0
		out = append(out, f)
	}
	return out, rows.Err()
}

func ListAllFoldersByOwner(ownerID string, trashed bool) ([]model.Folder, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`
		SELECT id, owner_id, name, parent_id, is_trashed, updated_at
		FROM folders
		WHERE owner_id = ? AND is_trashed = ?
		ORDER BY name
	`, ownerID, boolToInt(trashed))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.Folder{}
	for rows.Next() {
		var f model.Folder
		var trashedInt int
		if err := rows.Scan(&f.ID, &f.OwnerID, &f.Name, &f.ParentID, &trashedInt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.IsTrashed = trashedInt != 0
		out = append(out, f)
	}
	return out, rows.Err()
}

func GetFolderByID(ownerID, folderID string) (*model.Folder, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	var f model.Folder
	var trashedInt int
	err := dbConn.QueryRow(`
		SELECT id, owner_id, name, parent_id, is_trashed, updated_at
		FROM folders
		WHERE owner_id = ? AND id = ?
	`, ownerID, folderID).Scan(&f.ID, &f.OwnerID, &f.Name, &f.ParentID, &trashedInt, &f.UpdatedAt)
	if err != nil {
		return nil, err
	}
	f.IsTrashed = trashedInt != 0
	return &f, nil
}

func SetFolderTrashed(ownerID, folderID string, trashed bool) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`
		WITH RECURSIVE folder_tree(id) AS (
			SELECT id FROM folders WHERE owner_id = ? AND id = ?
			UNION ALL
			SELECT f.id FROM folders f
			JOIN folder_tree ft ON f.parent_id = ft.id
			WHERE f.owner_id = ?
		)
		UPDATE folders
		SET is_trashed = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id IN (SELECT id FROM folder_tree)
	`, ownerID, folderID, ownerID, boolToInt(trashed))
	return err
}

func ListFolderPath(ownerID, folderID string) ([]model.Folder, error) {
	if strings.TrimSpace(folderID) == "" {
		return nil, nil
	}
	path := []model.Folder{}
	cur := folderID
	for strings.TrimSpace(cur) != "" {
		f, err := GetFolderByID(ownerID, cur)
		if err != nil {
			break
		}
		path = append([]model.Folder{*f}, path...)
		cur = f.ParentID
	}
	return path, nil
}

func RenameFolder(ownerID, folderID, newName string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`
		UPDATE folders
		SET name = ?, updated_at = CURRENT_TIMESTAMP
		WHERE owner_id = ? AND id = ?
	`, newName, ownerID, folderID)
	return err
}

func MoveFolder(ownerID, folderID, parentID string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	if strings.TrimSpace(parentID) == "" {
		parentID = ""
	}
	_, err := dbConn.Exec(`
		UPDATE folders
		SET parent_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE owner_id = ? AND id = ?
	`, parentID, ownerID, folderID)
	return err
}

func TouchFolderAndAncestors(ownerID, folderID string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return nil
	}
	_, err := dbConn.Exec(`
		WITH RECURSIVE folder_line(id, parent_id) AS (
			SELECT id, parent_id
			FROM folders
			WHERE owner_id = ? AND id = ?
			UNION ALL
			SELECT f.id, f.parent_id
			FROM folders f
			JOIN folder_line fl ON f.id = fl.parent_id
			WHERE f.owner_id = ?
		)
		UPDATE folders
		SET updated_at = CURRENT_TIMESTAMP
		WHERE id IN (SELECT id FROM folder_line)
	`, ownerID, folderID, ownerID)
	return err
}

func IsFolderDescendant(ownerID, folderID, maybeDescendantID string) (bool, error) {
	if dbConn == nil {
		return false, fmt.Errorf("db is not initialized")
	}
	if folderID == "" || maybeDescendantID == "" {
		return false, nil
	}
	var n int
	err := dbConn.QueryRow(`
		WITH RECURSIVE folder_tree(id) AS (
			SELECT id FROM folders WHERE owner_id = ? AND id = ?
			UNION ALL
			SELECT f.id FROM folders f
			JOIN folder_tree ft ON f.parent_id = ft.id
			WHERE f.owner_id = ?
		)
		SELECT COUNT(1) FROM folder_tree WHERE id = ?
	`, ownerID, folderID, ownerID, maybeDescendantID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
