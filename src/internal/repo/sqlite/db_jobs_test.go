package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestSaveJobSingleAndSelectiveTags(t *testing.T) {
	projectRoot := t.TempDir()
	t.Cleanup(Close)

	if err := Init(projectRoot); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if _, err := dbConn.Exec(`INSERT INTO users(id, login_id, email, password_hash) VALUES ('user-1', 'user1', 'user1@example.com', 'hash')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := UpsertTag("user-1", "tag1", ""); err != nil {
		t.Fatalf("insert tag1: %v", err)
	}
	if err := UpsertTag("user-1", "tag2", ""); err != nil {
		t.Fatalf("insert tag2: %v", err)
	}

	job1 := &model.Job{
		StatusCode: 10,
		Filename:   "file1.wav",
		FileType:   "audio",
		UploadedTS: 100,
		OwnerID:    "user-1",
		Tags:       []string{"tag1"},
	}
	job2 := &model.Job{
		StatusCode: 20,
		Filename:   "file2.wav",
		FileType:   "audio",
		UploadedTS: 200,
		OwnerID:    "user-1",
		Tags:       []string{"tag2"},
	}

	// Save job1 and job2 individually.
	if err := SaveJob("job-1", job1); err != nil {
		t.Fatalf("save job-1: %v", err)
	}
	if err := SaveJob("job-2", job2); err != nil {
		t.Fatalf("save job-2: %v", err)
	}

	loaded, err := LoadJobs()
	if err != nil {
		t.Fatalf("load jobs: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(loaded))
	}
	if !reflect.DeepEqual(loaded["job-1"].Tags, []string{"tag1"}) {
		t.Fatalf("expected tag1 for job-1, got %v", loaded["job-1"].Tags)
	}
	if !reflect.DeepEqual(loaded["job-2"].Tags, []string{"tag2"}) {
		t.Fatalf("expected tag2 for job-2, got %v", loaded["job-2"].Tags)
	}

	// Update job-1 without touching job-2 tags.
	job1Updated := &model.Job{
		StatusCode:      50,
		Filename:        "file1-renamed.wav",
		FileType:        "audio",
		UploadedTS:      100,
		OwnerID:         "user-1",
		Tags:            []string{"tag1", "tag2"},
		ProgressPercent: 100,
	}
	if err := SaveJob("job-1", job1Updated); err != nil {
		t.Fatalf("update job-1: %v", err)
	}

	loadedAfter, err := LoadJobs()
	if err != nil {
		t.Fatalf("load jobs after update: %v", err)
	}
	if loadedAfter["job-1"].Filename != "file1-renamed.wav" {
		t.Fatalf("expected updated filename, got %s", loadedAfter["job-1"].Filename)
	}
	if !reflect.DeepEqual(loadedAfter["job-1"].Tags, []string{"tag1", "tag2"}) {
		t.Fatalf("expected tag1, tag2 for job-1, got %v", loadedAfter["job-1"].Tags)
	}
	// Verify job-2 tags were untouched.
	if !reflect.DeepEqual(loadedAfter["job-2"].Tags, []string{"tag2"}) {
		t.Fatalf("expected job-2 tags to remain tag2, got %v", loadedAfter["job-2"].Tags)
	}

	// Delete job-1 using DeleteJobs.
	if err := DeleteJobs([]string{"job-1"}); err != nil {
		t.Fatalf("delete job-1: %v", err)
	}
	loadedFinal, err := LoadJobs()
	if err != nil {
		t.Fatalf("load jobs after delete: %v", err)
	}
	if _, ok := loadedFinal["job-1"]; ok {
		t.Fatalf("job-1 should be deleted")
	}
	if _, ok := loadedFinal["job-2"]; !ok {
		t.Fatalf("job-2 should remain")
	}
}

func TestInitPragmasAndConnectionPool(t *testing.T) {
	projectRoot := t.TempDir()
	t.Cleanup(Close)

	if err := Init(projectRoot); err != nil {
		t.Fatalf("init db: %v", err)
	}

	var journalMode string
	if err := dbConn.QueryRow(`PRAGMA journal_mode;`).Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("expected WAL journal mode, got %s", journalMode)
	}

	var synchronous int
	if err := dbConn.QueryRow(`PRAGMA synchronous;`).Scan(&synchronous); err != nil {
		t.Fatalf("query synchronous: %v", err)
	}
	// PRAGMA synchronous: 1 = NORMAL
	if synchronous != 1 {
		t.Fatalf("expected synchronous=1 (NORMAL), got %d", synchronous)
	}

	var foreignKeys int
	if err := dbConn.QueryRow(`PRAGMA foreign_keys;`).Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("expected foreign_keys=1, got %d", foreignKeys)
	}

	stats := dbConn.Stats()
	if stats.MaxOpenConnections < 4 {
		t.Fatalf("expected MaxOpenConnections >= 4, got %d", stats.MaxOpenConnections)
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

func TestQueryJobsPagedAndBlobUsage(t *testing.T) {
	projectRoot := t.TempDir()
	t.Cleanup(Close)

	if err := Init(projectRoot); err != nil {
		t.Fatalf("init db: %v", err)
	}

	if _, err := dbConn.Exec(`INSERT INTO users(id, login_id, email, password_hash) VALUES ('user-1', 'user1', 'user1@example.com', 'hash')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := dbConn.Exec(`INSERT INTO folders(id, owner_id, name) VALUES ('f-1', 'user-1', 'Folder 1'), ('f-2', 'user-1', 'Folder 2')`); err != nil {
		t.Fatalf("insert folders: %v", err)
	}
	if err := UpsertTag("user-1", "tag-a", ""); err != nil {
		t.Fatalf("insert tag-a: %v", err)
	}
	if err := UpsertTag("user-1", "tag-b", ""); err != nil {
		t.Fatalf("insert tag-b: %v", err)
	}

	// Insert 5 jobs
	jobs := map[string]*model.Job{
		"job-1": {StatusCode: 50, Filename: "Lecture 1.mp3", FileType: "audio", OwnerID: "user-1", FolderID: "f-1", Tags: []string{"tag-a"}, UploadedTS: 100},
		"job-2": {StatusCode: 20, Filename: "Lecture 2.mp3", FileType: "audio", OwnerID: "user-1", FolderID: "f-1", Tags: []string{"tag-b"}, UploadedTS: 200},
		"job-3": {StatusCode: 50, Filename: "Seminar.mp3", FileType: "audio", OwnerID: "user-1", FolderID: "f-2", Tags: []string{"tag-a"}, UploadedTS: 300},
		"job-4": {StatusCode: 10, Filename: "Meeting.mp3", FileType: "audio", OwnerID: "user-1", FolderID: "", UploadedTS: 400},
		"job-5": {StatusCode: 50, Filename: "Old.mp3", FileType: "audio", OwnerID: "user-1", FolderID: "", IsTrashed: true, UploadedTS: 500},
	}
	for id, job := range jobs {
		if err := SaveJob(id, job); err != nil {
			t.Fatalf("save job %s: %v", id, err)
		}
	}

	// Save blobs for job-1 and job-2
	if err := SaveJobBinaryBlob("job-1", BlobKindPreview, []byte("preview-data")); err != nil {
		t.Fatalf("save preview: %v", err)
	}
	if err := SaveJobBinaryBlob("job-2", BlobKindAudioAAC, []byte("audio-bytes-12345")); err != nil {
		t.Fatalf("save audio: %v", err)
	}

	// 1. Query all untrashed jobs (FilterFolder=false)
	res, err := QueryJobsPaged(JobQueryFilter{
		OwnerID:   "user-1",
		IsTrashed: false,
		Page:      1,
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("query untrashed: %v", err)
	}
	if res.TotalItems != 4 || len(res.Records) != 4 {
		t.Fatalf("expected 4 untrashed jobs, got total=%d len=%d", res.TotalItems, len(res.Records))
	}
	// Verify sorted by uploaded_ts DESC (job-4, job-3, job-2, job-1) and IDs present
	if res.Records[0].Job.Filename != "Meeting.mp3" || res.Records[0].ID != "job-4" ||
		res.Records[1].Job.Filename != "Seminar.mp3" || res.Records[1].ID != "job-3" {
		t.Fatalf("unexpected sort order: %+v, %+v", res.Records[0], res.Records[1])
	}

	// 2. Query folder 'f-1'
	res, err = QueryJobsPaged(JobQueryFilter{
		OwnerID:      "user-1",
		FolderID:     "f-1",
		FilterFolder: true,
		IsTrashed:    false,
		Page:         1,
		PageSize:     10,
	})
	if err != nil {
		t.Fatalf("query folder f-1: %v", err)
	}
	if res.TotalItems != 2 || len(res.Records) != 2 {
		t.Fatalf("expected 2 jobs in f-1, got total=%d", res.TotalItems)
	}

	// 3. Query root folder (folder="")
	res, err = QueryJobsPaged(JobQueryFilter{
		OwnerID:      "user-1",
		FolderID:     "",
		FilterFolder: true,
		IsTrashed:    false,
		Page:         1,
		PageSize:     10,
	})
	if err != nil {
		t.Fatalf("query root folder: %v", err)
	}
	if res.TotalItems != 1 || res.Records[0].Job.Filename != "Meeting.mp3" {
		t.Fatalf("expected 1 root job (Meeting.mp3), got total=%d", res.TotalItems)
	}

	// 4. Query by tag 'tag-a'
	res, err = QueryJobsPaged(JobQueryFilter{
		OwnerID:   "user-1",
		Tag:       "tag-a",
		IsTrashed: false,
		Page:      1,
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("query tag-a: %v", err)
	}
	if res.TotalItems != 2 {
		t.Fatalf("expected 2 jobs with tag-a, got total=%d", res.TotalItems)
	}

	// 5. Query by search query 'lecture'
	res, err = QueryJobsPaged(JobQueryFilter{
		OwnerID:     "user-1",
		SearchQuery: "lecture",
		IsTrashed:   false,
		Page:        1,
		PageSize:    10,
	})
	if err != nil {
		t.Fatalf("query search lecture: %v", err)
	}
	if res.TotalItems != 2 {
		t.Fatalf("expected 2 jobs matching lecture, got total=%d", res.TotalItems)
	}

	// 6. Pagination (Page=1, PageSize=2)
	res, err = QueryJobsPaged(JobQueryFilter{
		OwnerID:   "user-1",
		IsTrashed: false,
		Page:      1,
		PageSize:  2,
	})
	if err != nil {
		t.Fatalf("query page 1: %v", err)
	}
	if res.TotalPages != 2 || len(res.Records) != 2 {
		t.Fatalf("expected TotalPages=2, len=2, got tp=%d len=%d", res.TotalPages, len(res.Records))
	}

	// 7. Query trashed
	res, err = QueryJobsPaged(JobQueryFilter{
		OwnerID:   "user-1",
		IsTrashed: true,
		Page:      1,
		PageSize:  10,
	})
	if err != nil {
		t.Fatalf("query trashed: %v", err)
	}
	if res.TotalItems != 1 || res.Records[0].Job.Filename != "Old.mp3" || res.Records[0].ID != "job-5" {
		t.Fatalf("expected 1 trashed job (Old.mp3), got total=%d", res.TotalItems)
	}

	// 8. Test JobBlobUsageMapForJobIDs
	sizeMap, err := JobBlobUsageMapForJobIDs([]string{"job-1", "job-2", "job-3"})
	if err != nil {
		t.Fatalf("blob usage map: %v", err)
	}
	if sizeMap["job-1"] != int64(len("preview-data")) {
		t.Fatalf("expected preview-data size %d, got %d", len("preview-data"), sizeMap["job-1"])
	}
	if sizeMap["job-2"] != int64(len("audio-bytes-12345")) {
		t.Fatalf("expected audio size %d, got %d", len("audio-bytes-12345"), sizeMap["job-2"])
	}
	if sizeMap["job-3"] != 0 {
		t.Fatalf("expected job-3 size 0, got %d", sizeMap["job-3"])
	}
}

func TestPhase32Queries(t *testing.T) {
	projectRoot := t.TempDir()
	t.Cleanup(func() {
		Close()
	})

	if err := Init(projectRoot); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := dbConn.Exec(`INSERT INTO users(id, login_id, email, password_hash) VALUES ('u1', 'user1', 'u1@example.com', 'hash')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := dbConn.Exec(`INSERT INTO folders(id, name, owner_id, parent_id) VALUES ('f1', 'Folder1', 'u1', NULL)`); err != nil {
		t.Fatalf("insert f1: %v", err)
	}
	if _, err := dbConn.Exec(`INSERT INTO folders(id, name, owner_id, parent_id) VALUES ('f2', 'Folder2', 'u1', NULL)`); err != nil {
		t.Fatalf("insert f2: %v", err)
	}
	if _, err := dbConn.Exec(`INSERT INTO tags(id, owner_id, name, description) VALUES ('t1', 'u1', 'doneTag', '')`); err != nil {
		t.Fatalf("insert tag: %v", err)
	}

	activeJob := &model.Job{
		StatusCode: 10,
		Filename:   "active.mp3",
		FileType:   "audio/mp3",
		OwnerID:    "u1",
		FolderID:   "f1",
		Tags:       []string{"activeTag"},
	}
	completedJob := &model.Job{
		StatusCode: 50,
		Filename:   "completed.mp3",
		FileType:   "audio/mp3",
		OwnerID:    "u1",
		FolderID:   "f1",
		Tags:       []string{"doneTag"},
	}
	trashedJob := &model.Job{
		StatusCode: 50,
		Filename:   "trashed.mp3",
		FileType:   "audio/mp3",
		OwnerID:    "u1",
		FolderID:   "f2",
		IsTrashed:  true,
	}

	if err := SaveJob("job-act", activeJob); err != nil {
		t.Fatalf("save active: %v", err)
	}
	if err := SaveJob("job-done", completedJob); err != nil {
		t.Fatalf("save completed: %v", err)
	}
	if err := SaveJob("job-trash", trashedJob); err != nil {
		t.Fatalf("save trashed: %v", err)
	}

	// 1. Test GetJobByID
	gotDone, err := GetJobByID("job-done")
	if err != nil {
		t.Fatalf("GetJobByID job-done: %v", err)
	}
	if gotDone == nil || gotDone.Filename != "completed.mp3" || gotDone.StatusCode != 50 {
		t.Fatalf("unexpected gotDone: %+v", gotDone)
	}
	if len(gotDone.Tags) != 1 || gotDone.Tags[0] != "doneTag" {
		t.Fatalf("unexpected tags for gotDone: %v", gotDone.Tags)
	}

	gotMissing, err := GetJobByID("non-existent")
	if err != nil {
		t.Fatalf("GetJobByID missing err: %v", err)
	}
	if gotMissing != nil {
		t.Fatalf("expected nil for missing job, got %+v", gotMissing)
	}

	// 2. Test LoadActiveJobs
	actives, err := LoadActiveJobs()
	if err != nil {
		t.Fatalf("LoadActiveJobs: %v", err)
	}
	if len(actives) != 1 || actives["job-act"] == nil {
		t.Fatalf("expected only job-act in LoadActiveJobs, got %v", actives)
	}

	// 3. Test ListJobIDsByFolderIDs
	f1IDs, err := ListJobIDsByFolderIDs("u1", []string{"f1"})
	if err != nil {
		t.Fatalf("ListJobIDsByFolderIDs: %v", err)
	}
	if len(f1IDs) != 2 { // job-act, job-done
		t.Fatalf("expected 2 jobs in f1, got %v", f1IDs)
	}

	// 4. Test ListCompletedJobsByFolderIDs
	doneRecords, err := ListCompletedJobsByFolderIDs("u1", []string{"f1"})
	if err != nil {
		t.Fatalf("ListCompletedJobsByFolderIDs: %v", err)
	}
	if len(doneRecords) != 1 || doneRecords[0].ID != "job-done" || doneRecords[0].Job.Filename != "completed.mp3" {
		t.Fatalf("expected job-done in completed records, got %v", doneRecords)
	}

	// 5. Test ListTrashedJobIDs
	trashIDs, err := ListTrashedJobIDs("u1")
	if err != nil {
		t.Fatalf("ListTrashedJobIDs: %v", err)
	}
	if len(trashIDs) != 1 || trashIDs[0] != "job-trash" {
		t.Fatalf("expected [job-trash], got %v", trashIDs)
	}

	// 6. Test GetOwnerIDsByJobIDs
	owners, err := GetOwnerIDsByJobIDs([]string{"job-act", "job-done", "non-existent"})
	if err != nil {
		t.Fatalf("GetOwnerIDsByJobIDs: %v", err)
	}
	if len(owners) != 1 || owners[0] != "u1" {
		t.Fatalf("expected [u1], got %v", owners)
	}

	// 7. Test FilterJobIDsByOwner
	allOwned, err := FilterJobIDsByOwner("u1", []string{"job-act", "job-done", "job-trash"}, false)
	if err != nil {
		t.Fatalf("FilterJobIDsByOwner: %v", err)
	}
	if len(allOwned) != 3 {
		t.Fatalf("expected 3 jobs owned by u1, got %v", allOwned)
	}

	trashedOwned, err := FilterJobIDsByOwner("u1", []string{"job-act", "job-done", "job-trash"}, true)
	if err != nil {
		t.Fatalf("FilterJobIDsByOwner trashedOnly: %v", err)
	}
	if len(trashedOwned) != 1 || trashedOwned[0] != "job-trash" {
		t.Fatalf("expected [job-trash], got %v", trashedOwned)
	}
}
