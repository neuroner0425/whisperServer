package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	model "whisperserver/src/internal/domain"

	_ "modernc.org/sqlite"
)

func openTempSQLiteDB(t *testing.T, projectRoot string) (*sql.DB, string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".run"), 0o755); err != nil {
		t.Fatalf("mkdir .run: %v", err)
	}
	dbPath := filepath.Join(projectRoot, ".run", "whisper.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	return db, dbPath
}

func execStatements(t *testing.T, db *sql.DB, statements []string) {
	t.Helper()
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec statement failed: %v\nsql: %s", err, stmt)
		}
	}
}

func TestInitMigratesLegacyJobTags(t *testing.T) {
	projectRoot := t.TempDir()
	legacyDB, _ := openTempSQLiteDB(t, projectRoot)
	t.Cleanup(func() {
		if legacyDB != nil {
			_ = legacyDB.Close()
		}
		Close()
	})

	statements := []string{
		`CREATE TABLE status_codes (code INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);`,
		`INSERT INTO status_codes(code, name) VALUES (10, '작업 대기 중');`,
		`CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL DEFAULT '',
			file_type TEXT NOT NULL DEFAULT '',
			uploaded_at TEXT NOT NULL DEFAULT '',
			uploaded_ts REAL NOT NULL DEFAULT 0,
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
			progress_percent INTEGER NOT NULL DEFAULT 0
		);`,
		`INSERT INTO jobs(
			id, status, filename, file_type, uploaded_ts, description, owner_id, tags_json, progress_percent
		) VALUES (
			'job-1', '완료', 'demo.wav', 'audio', 123, 'desc', 'user-1', '["alpha","beta"]', 80
		);`,
	}
	execStatements(t, legacyDB, statements)
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}
	legacyDB = nil

	if err := Init(projectRoot); err != nil {
		t.Fatalf("init migrated db: %v", err)
	}

	hasTagsJSON, err := columnExists(dbConn, "jobs", "tags_json")
	if err != nil {
		t.Fatalf("check tags_json column: %v", err)
	}
	if hasTagsJSON {
		t.Fatal("expected jobs.tags_json to be removed after migration")
	}

	rows, err := dbConn.Query(`
		SELECT jt.job_id, t.name, jt.position
		FROM job_tags jt
		JOIN tags t ON t.id = jt.tag_id
		ORDER BY jt.position
	`)
	if err != nil {
		t.Fatalf("query job_tags: %v", err)
	}
	defer rows.Close()

	type jobTag struct {
		JobID    string
		TagName  string
		Position int
	}
	var got []jobTag
	for rows.Next() {
		var item jobTag
		if err := rows.Scan(&item.JobID, &item.TagName, &item.Position); err != nil {
			t.Fatalf("scan job_tags: %v", err)
		}
		got = append(got, item)
	}
	want := []jobTag{
		{JobID: "job-1", TagName: "alpha", Position: 0},
		{JobID: "job-1", TagName: "beta", Position: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected migrated tags: got=%v want=%v", got, want)
	}
}

func TestSaveLoadJobsWithJobTags(t *testing.T) {
	projectRoot := t.TempDir()
	t.Cleanup(Close)

	if err := Init(projectRoot); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if _, err := dbConn.Exec(`INSERT INTO users(id, login_id, email, password_hash) VALUES ('user-1', 'user1', 'user1@example.com', 'hash')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := dbConn.Exec(`INSERT INTO folders(id, owner_id, name) VALUES ('folder-1', 'user-1', 'Folder')`); err != nil {
		t.Fatalf("insert folder: %v", err)
	}
	if err := UpsertTag("user-1", "alpha", ""); err != nil {
		t.Fatalf("insert alpha tag: %v", err)
	}
	if err := UpsertTag("user-1", "beta", ""); err != nil {
		t.Fatalf("insert beta tag: %v", err)
	}

	snapshot := map[string]*model.Job{
		"job-1": {
			StatusCode:      50,
			Filename:        "demo.wav",
			FileType:        "audio",
			UploadedTS:      123,
			Description:     "desc",
			RefineEnabled:   true,
			OwnerID:         "user-1",
			Tags:            []string{"alpha", "beta"},
			FolderID:        "folder-1",
			ProgressPercent: 80,
		},
	}
	if err := SaveJobs(snapshot); err != nil {
		t.Fatalf("save jobs: %v", err)
	}

	got, err := LoadJobs()
	if err != nil {
		t.Fatalf("load jobs: %v", err)
	}
	job := got["job-1"]
	if job == nil {
		t.Fatal("expected saved job to be loaded")
	}
	if !reflect.DeepEqual(job.Tags, []string{"alpha", "beta"}) {
		t.Fatalf("unexpected tags: got=%v", job.Tags)
	}
}

func TestRuntimeArtifactsMoveToFilesystem(t *testing.T) {
	projectRoot := t.TempDir()
	db, _ := openTempSQLiteDB(t, projectRoot)
	t.Cleanup(func() {
		if db != nil {
			_ = db.Close()
		}
		Close()
	})

	statements := []string{
		`CREATE TABLE status_codes (code INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);`,
		`INSERT INTO status_codes(code, name) VALUES (50, '완료');`,
		`CREATE TABLE users (id TEXT PRIMARY KEY, login_id TEXT, email TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE folders (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, name TEXT NOT NULL, parent_id TEXT DEFAULT NULL, is_trashed INTEGER NOT NULL DEFAULT 0, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			status_code INTEGER NOT NULL DEFAULT 50,
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
			progress_percent INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE tags (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE job_blobs (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data BLOB NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind)
		);`,
		`INSERT INTO users(id, login_id, email, password_hash) VALUES ('user-1', 'user1', 'user1@example.com', 'hash');`,
		`INSERT INTO jobs(id, filename, file_type, owner_id) VALUES ('job-1', 'demo.pdf', 'pdf', 'user-1');`,
		`INSERT INTO job_blobs(job_id, kind, data) VALUES ('job-1', 'preview', 'preview text');`,
		`INSERT INTO job_blobs(job_id, kind, data) VALUES ('job-1', 'document_chunk_index', '{"last_completed_chunk":1}');`,
		`INSERT INTO job_blobs(job_id, kind, data) VALUES ('job-1', 'document_chunk_1_json', '{"page":1}');`,
	}
	execStatements(t, db, statements)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	db = nil

	if err := Init(projectRoot); err != nil {
		t.Fatalf("init db: %v", err)
	}

	for _, kind := range []string{BlobKindPreview, BlobKindDocumentChunkIndex, "document_chunk_1_json"} {
		if !HasJobBlob("job-1", kind) {
			t.Fatalf("expected runtime artifact %s to be available from filesystem", kind)
		}
		var count int
		if err := dbConn.QueryRow(`SELECT COUNT(1) FROM job_blobs WHERE job_id = 'job-1' AND kind = ?`, kind).Scan(&count); err != nil {
			t.Fatalf("count runtime artifact %s: %v", kind, err)
		}
		if count != 0 {
			t.Fatalf("expected runtime artifact %s to be removed from db", kind)
		}
	}
}

func TestInitMigratesLegacyJobJSONArtifacts(t *testing.T) {
	projectRoot := t.TempDir()
	legacyDB, _ := openTempSQLiteDB(t, projectRoot)
	t.Cleanup(func() {
		if legacyDB != nil {
			_ = legacyDB.Close()
		}
		Close()
	})

	statements := []string{
		`CREATE TABLE status_codes (code INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);`,
		`INSERT INTO status_codes(code, name) VALUES (50, '완료');`,
		`CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			status_code INTEGER NOT NULL DEFAULT 50,
			filename TEXT NOT NULL DEFAULT '',
			file_type TEXT NOT NULL DEFAULT '',
			uploaded_ts REAL NOT NULL DEFAULT 0,
			media_duration_seconds INTEGER,
			description TEXT NOT NULL DEFAULT '',
			refine_enabled INTEGER NOT NULL DEFAULT 0,
			owner_id TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			is_trashed INTEGER NOT NULL DEFAULT 0,
			deleted_ts REAL NOT NULL DEFAULT 0,
			started_ts REAL NOT NULL DEFAULT 0,
			completed_ts REAL NOT NULL DEFAULT 0,
			progress_percent INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE job_blobs (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data BLOB NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind)
		);`,
		`INSERT INTO jobs(id, filename, file_type, owner_id) VALUES ('job-1', 'demo.wav', 'audio', 'user-1');`,
		`INSERT INTO job_blobs(job_id, kind, data) VALUES ('job-1', 'transcript_json', '{"segments":[{"from":"00:00:00,000","to":"00:00:01,000","text":"hello"}]}');`,
		`INSERT INTO job_blobs(job_id, kind, data) VALUES ('job-1', 'refined', '{"paragraph":[{"paragraph_summary":"요약","sentence":[{"start_time":"[00:00:00,000]","content":"안녕하세요"}]}]}');`,
		`INSERT INTO job_blobs(job_id, kind, data) VALUES ('job-1', 'document_json', '{"pages":[]}');`,
		`INSERT INTO job_blobs(job_id, kind, data) VALUES ('job-1', 'document_markdown', '# old');`,
		`INSERT INTO job_blobs(job_id, kind, data) VALUES ('job-1', 'transcript', 'legacy transcript');`,
	}
	execStatements(t, legacyDB, statements)
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}
	legacyDB = nil

	if err := Init(projectRoot); err != nil {
		t.Fatalf("init migrated db: %v", err)
	}

	var jsonCount int
	if err := dbConn.QueryRow(`SELECT COUNT(1) FROM job_json WHERE job_id = 'job-1'`).Scan(&jsonCount); err != nil {
		t.Fatalf("count job_json: %v", err)
	}
	if jsonCount != 3 {
		t.Fatalf("expected 3 migrated json rows, got %d", jsonCount)
	}

	for _, kind := range []string{"transcript_json", "refined", "document_json", "document_markdown", "transcript"} {
		var count int
		if err := dbConn.QueryRow(`SELECT COUNT(1) FROM job_blobs WHERE job_id = 'job-1' AND kind = ?`, kind).Scan(&count); err != nil {
			t.Fatalf("count legacy blob kind %s: %v", kind, err)
		}
		if count != 0 {
			t.Fatalf("expected legacy blob kind %s to be removed", kind)
		}
	}
}

func TestInitRepairsBrokenLegacyJobForeignKeys(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".run"), 0o755); err != nil {
		t.Fatalf("mkdir .run: %v", err)
	}
	dbPath := filepath.Join(projectRoot, ".run", "whisper.db")

	brokenDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open broken db: %v", err)
	}
	t.Cleanup(func() {
		if brokenDB != nil {
			_ = brokenDB.Close()
		}
		Close()
	})

	statements := []string{
		`CREATE TABLE status_codes (code INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);`,
		`INSERT INTO status_codes(code, name) VALUES (50, '완료');`,
		`CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			status_code INTEGER NOT NULL DEFAULT 50,
			filename TEXT NOT NULL DEFAULT '',
			file_type TEXT NOT NULL DEFAULT '',
			uploaded_ts REAL NOT NULL DEFAULT 0,
			media_duration_seconds INTEGER,
			description TEXT NOT NULL DEFAULT '',
			refine_enabled INTEGER NOT NULL DEFAULT 0,
			owner_id TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			is_trashed INTEGER NOT NULL DEFAULT 0,
			deleted_ts REAL NOT NULL DEFAULT 0,
			started_ts REAL NOT NULL DEFAULT 0,
			completed_ts REAL NOT NULL DEFAULT 0,
			progress_percent INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (status_code) REFERENCES status_codes(code)
		);`,
		`CREATE TABLE job_blobs (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data BLOB NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind),
			FOREIGN KEY (job_id) REFERENCES "jobs_legacy"(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE tags (
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (owner_id, name)
		);`,
		`CREATE TABLE job_tags (
			job_id TEXT NOT NULL,
			tag_name TEXT NOT NULL,
			position INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, tag_name),
			FOREIGN KEY (job_id) REFERENCES "jobs_legacy"(id) ON DELETE CASCADE
		);`,
		`INSERT INTO jobs(id, filename, file_type, owner_id) VALUES ('job-1', 'demo.wav', 'audio', 'user-1');`,
		`INSERT INTO job_blobs(job_id, kind, data) VALUES ('job-1', 'transcript_json', '{"segments":[]}');`,
		`INSERT INTO job_tags(job_id, tag_name, position) VALUES ('job-1', 'alpha', 0);`,
	}
	for _, stmt := range statements {
		if _, err := brokenDB.Exec(stmt); err != nil {
			t.Fatalf("seed broken schema: %v", err)
		}
	}
	if err := brokenDB.Close(); err != nil {
		t.Fatalf("close broken db: %v", err)
	}
	brokenDB = nil

	if err := Init(projectRoot); err != nil {
		t.Fatalf("init repaired db: %v", err)
	}

	jobTagsBroken, err := foreignKeyReferencesTable(dbConn, "job_tags", "jobs_legacy")
	if err != nil {
		t.Fatalf("check job_tags fk: %v", err)
	}
	if jobTagsBroken {
		t.Fatal("expected job_tags foreign key to be repaired")
	}
	jobBlobsBroken, err := foreignKeyReferencesTable(dbConn, "job_blobs", "jobs_legacy")
	if err != nil {
		t.Fatalf("check job_blobs fk: %v", err)
	}
	if jobBlobsBroken {
		t.Fatal("expected job_blobs foreign key to be repaired")
	}
}

func TestInitAppliesOneTimeMaintenance(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".run"), 0o755); err != nil {
		t.Fatalf("mkdir .run: %v", err)
	}
	dbPath := filepath.Join(projectRoot, ".run", "whisper.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if db != nil {
			_ = db.Close()
		}
		Close()
	})

	statements := []string{
		`CREATE TABLE status_codes (code INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);`,
		`INSERT INTO status_codes(code, name) VALUES (50, '완료'), (60, '실패');`,
		`CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			status_code INTEGER NOT NULL DEFAULT 60,
			filename TEXT NOT NULL DEFAULT '',
			file_type TEXT NOT NULL DEFAULT '',
			uploaded_ts REAL NOT NULL DEFAULT 0,
			media_duration_seconds INTEGER,
			description TEXT NOT NULL DEFAULT '',
			refine_enabled INTEGER NOT NULL DEFAULT 0,
			owner_id TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			is_trashed INTEGER NOT NULL DEFAULT 0,
			deleted_ts REAL NOT NULL DEFAULT 0,
			started_ts REAL NOT NULL DEFAULT 0,
			completed_ts REAL NOT NULL DEFAULT 0,
			progress_percent INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE job_blobs (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data BLOB NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind)
		);`,
		`CREATE TABLE job_json (
			job_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			data TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (job_id, kind)
		);`,
		`INSERT INTO jobs(id, filename, file_type, owner_id, status_code) VALUES
			('audio-1', 'demo.wav', 'audio', 'user-1', 60),
			('pdf-1', 'demo.pdf', 'pdf', 'user-1', 60);`,
		`INSERT INTO job_json(job_id, kind, data) VALUES
			('audio-1', 'transcript_json', '{"segments":[]}'),
			('pdf-1', 'document_json', '{"pages":[]}');`,
		`INSERT INTO job_blobs(job_id, kind, data) VALUES
			('audio-1', 'transcript', 'old transcript'),
			('pdf-1', 'document_markdown', '# old markdown'),
			('pdf-1', 'document_chunk_index', '{"last_completed_chunk":1}'),
			('pdf-1', 'document_chunk_1_json', '{"page":1}');`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed db: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	db = nil

	if err := Init(projectRoot); err != nil {
		t.Fatalf("init db: %v", err)
	}

	var audioStatus, pdfStatus int
	if err := dbConn.QueryRow(`SELECT status_code FROM jobs WHERE id = 'audio-1'`).Scan(&audioStatus); err != nil {
		t.Fatalf("query audio status: %v", err)
	}
	if err := dbConn.QueryRow(`SELECT status_code FROM jobs WHERE id = 'pdf-1'`).Scan(&pdfStatus); err != nil {
		t.Fatalf("query pdf status: %v", err)
	}
	if audioStatus != model.JobStatusCompletedCode || pdfStatus != model.JobStatusCompletedCode {
		t.Fatalf("expected statuses to be reconciled to completed, got audio=%d pdf=%d", audioStatus, pdfStatus)
	}

	for _, kind := range []string{"transcript", "document_markdown", "document_chunk_index", "document_chunk_1_json"} {
		var count int
		if err := dbConn.QueryRow(`SELECT COUNT(1) FROM job_blobs WHERE kind = ?`, kind).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", kind, err)
		}
		if count != 0 {
			t.Fatalf("expected obsolete blob kind %s to be removed", kind)
		}
	}

	version, err := currentDBMaintenanceVersion(dbConn)
	if err != nil {
		t.Fatalf("read maintenance version: %v", err)
	}
	if version != dbMaintenanceVersion {
		t.Fatalf("unexpected maintenance version: got=%d want=%d", version, dbMaintenanceVersion)
	}
}
