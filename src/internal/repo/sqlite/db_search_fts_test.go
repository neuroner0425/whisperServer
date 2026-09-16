package sqlite

import (
	"os"
	"path/filepath"
	"testing"

	model "whisperserver/src/internal/domain"
)

func TestFTS5AndGzipCompression(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".run"), 0o755); err != nil {
		t.Fatalf("mkdir .run: %v", err)
	}

	if err := Init(projectRoot); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// 1. Seed user and job
	_, err := dbConn.Exec(`INSERT INTO users(id, email, password_hash) VALUES ('user-1', 'test@test.com', 'hash')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = dbConn.Exec(`INSERT INTO jobs(id, filename, file_type, owner_id, status_code) VALUES ('job-1', 'lecture.mp3', 'audio', 'user-1', 50)`)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}

	// 2. Test SaveJobJSON with Gzip transparent compression and auto FTS indexing
	rawTranscript := `{"segments":[{"from":"00:00:01,000","to":"00:00:05,000","text":"안녕하세요 오늘 자료구조 수업을 시작합니다."}]}`
	if err := SaveJobJSON("job-1", BlobKindTranscriptJSON, rawTranscript); err != nil {
		t.Fatalf("SaveJobJSON failed: %v", err)
	}

	// Verify that data stored on disk is indeed gzip-compressed (magic bytes 0x1f, 0x8b)
	var rawBytes []byte
	if err := dbConn.QueryRow(`SELECT data FROM job_json WHERE job_id = 'job-1' AND kind = ?`, BlobKindTranscriptJSON).Scan(&rawBytes); err != nil {
		t.Fatalf("query raw blob: %v", err)
	}
	if len(rawBytes) < 2 || rawBytes[0] != 0x1f || rawBytes[1] != 0x8b {
		t.Fatalf("expected gzip magic header (0x1f, 0x8b), got %v", rawBytes[:2])
	}

	// Verify transparent decompression via LoadJobJSON
	loaded, err := LoadJobJSON("job-1", BlobKindTranscriptJSON)
	if err != nil {
		t.Fatalf("LoadJobJSON failed: %v", err)
	}
	if loaded != rawTranscript {
		t.Fatalf("expected %s, got %s", rawTranscript, loaded)
	}

	// 3. Test FTS5 search for Korean keyword
	jobIDs, err := SearchJobIDsByContent("user-1", "자료구조")
	if err != nil {
		t.Fatalf("SearchJobIDsByContent failed: %v", err)
	}
	if len(jobIDs) != 1 || jobIDs[0] != "job-1" {
		t.Fatalf("expected ['job-1'], got %v", jobIDs)
	}

	// Partial search / prefix
	jobIDs, err = SearchJobIDsByContent("user-1", "자료")
	if err != nil {
		t.Fatalf("SearchJobIDsByContent prefix failed: %v", err)
	}
	if len(jobIDs) != 1 || jobIDs[0] != "job-1" {
		t.Fatalf("expected ['job-1'], got %v", jobIDs)
	}

	// Non-matching search
	jobIDs, err = SearchJobIDsByContent("user-1", "운영체제")
	if err != nil {
		t.Fatalf("SearchJobIDsByContent failed: %v", err)
	}
	if len(jobIDs) != 0 {
		t.Fatalf("expected 0 matches, got %v", jobIDs)
	}

	// Punctuation search (e.g. Node.js should match even with dot)
	_ = IndexJobSearchContent("job-node", "user-1", "Learning Node.js backend development")
	jobIDs, err = SearchJobIDsByContent("user-1", "Node.js")
	if err != nil {
		t.Fatalf("SearchJobIDsByContent with dot failed: %v", err)
	}
	if len(jobIDs) != 1 || jobIDs[0] != "job-node" {
		t.Fatalf("expected ['job-node'], got %v", jobIDs)
	}

	// 4. Test QueryJobsPaged integrates FTS search and respects owner_id isolation
	paged, err := QueryJobsPaged(JobQueryFilter{
		OwnerID:     "user-1",
		SearchQuery: "자료구조",
	})
	if err != nil {
		t.Fatalf("QueryJobsPaged failed: %v", err)
	}
	if paged.TotalItems != 1 || len(paged.Records) != 1 || paged.Records[0].ID != "job-1" {
		t.Fatalf("expected job-1 in paged results, got %+v", paged)
	}

	// Another user searching for same keyword should return 0
	pagedUser2, err := QueryJobsPaged(JobQueryFilter{
		OwnerID:     "user-2",
		SearchQuery: "자료구조",
	})
	if err != nil {
		t.Fatalf("QueryJobsPaged user-2 failed: %v", err)
	}
	if pagedUser2.TotalItems != 0 {
		t.Fatalf("expected 0 items for user-2, got %d", pagedUser2.TotalItems)
	}

	// 5. Test DeleteJobs cleans up both job_json and FTS5 index
	if err := DeleteJobs([]string{"job-1"}); err != nil {
		t.Fatalf("DeleteJobs failed: %v", err)
	}

	var jsonCount, ftsCount int
	_ = dbConn.QueryRow(`SELECT count(1) FROM job_json WHERE job_id = 'job-1'`).Scan(&jsonCount)
	_ = dbConn.QueryRow(`SELECT count(1) FROM job_search_fts WHERE job_id = 'job-1'`).Scan(&ftsCount)
	if jsonCount != 0 {
		t.Fatalf("expected job_json to be 0, got %d", jsonCount)
	}
	if ftsCount != 0 {
		t.Fatalf("expected job_search_fts to be 0, got %d", ftsCount)
	}
}

func TestCleanupOrphanAndTemporaryArtifacts(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".run"), 0o755); err != nil {
		t.Fatalf("mkdir .run: %v", err)
	}
	if err := Init(projectRoot); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Seed completed job and orphaned data
	_, _ = dbConn.Exec(`INSERT INTO users(id, email, password_hash) VALUES ('u1', 'u1@test.com', 'h')`)
	_, _ = dbConn.Exec(`INSERT INTO jobs(id, filename, file_type, owner_id, status_code) VALUES ('job-done', 'f.mp3', 'audio', 'u1', 50)`)

	// 1. Completed job has refined_timeline that should be cleaned
	_ = SaveJobJSON("job-done", BlobKindRefinedTimeline, "timeline cache")
	// 2. Orphan job_json
	_, _ = dbConn.Exec(`INSERT INTO job_json(job_id, kind, data) VALUES ('orphan-job', 'transcript_json', 'raw')`)
	// 3. Diagnostics
	_, _ = dbConn.Exec(`INSERT INTO job_json(job_id, kind, data) VALUES ('job-done', 'refine_diagnostics_20260915_120000', '{"diag":true}')`)

	if err := cleanupOrphanAndTemporaryArtifacts(dbConn); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	var cnt int
	_ = dbConn.QueryRow(`SELECT count(1) FROM job_json WHERE kind = 'refined_timeline' AND job_id = 'job-done'`).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("expected refined_timeline to be cleaned, got %d", cnt)
	}

	_ = dbConn.QueryRow(`SELECT count(1) FROM job_json WHERE job_id = 'orphan-job'`).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("expected orphan-job to be cleaned, got %d", cnt)
	}

	_ = dbConn.QueryRow(`SELECT count(1) FROM job_json WHERE kind LIKE 'refine_diagnostics%'`).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("expected diagnostics to be cleaned, got %d", cnt)
	}
}

func TestSyncExistingJobsToFTS_HandlesEmptyContent(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".run"), 0o755); err != nil {
		t.Fatalf("mkdir .run: %v", err)
	}
	if err := Init(projectRoot); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Job with empty/unextractable json
	_, _ = dbConn.Exec(`INSERT INTO users(id, email, password_hash) VALUES ('u1', 'u1@test.com', 'h')`)
	_, _ = dbConn.Exec(`INSERT INTO jobs(id, filename, file_type, owner_id, status_code) VALUES ('job-empty', 'empty.mp3', 'audio', 'u1', 50)`)
	_ = SaveJobJSON("job-empty", BlobKindTranscriptJSON, `{"segments":[]}`)

	// First sync
	if err := SyncExistingJobsToFTS(dbConn); err != nil {
		t.Fatalf("first sync failed: %v", err)
	}

	// Verify it was indexed (even as empty content) so it won't be considered unindexed
	var unindexedCount int
	_ = dbConn.QueryRow(`SELECT count(1) FROM jobs WHERE id NOT IN (SELECT job_id FROM job_search_fts)`).Scan(&unindexedCount)
	if unindexedCount != 0 {
		t.Fatalf("expected 0 unindexed jobs after sync, got %d", unindexedCount)
	}

	// Second sync should find nothing to do
	if err := SyncExistingJobsToFTS(dbConn); err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
}

func TestSaveJob_SyncsFTSOwnerID(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".run"), 0o755); err != nil {
		t.Fatalf("mkdir .run: %v", err)
	}
	if err := Init(projectRoot); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	_, _ = dbConn.Exec(`INSERT INTO users(id, email, password_hash) VALUES ('u1', 'u1@test.com', 'h')`)
	_, _ = dbConn.Exec(`INSERT INTO users(id, email, password_hash) VALUES ('u2', 'u2@test.com', 'h')`)
	_, _ = dbConn.Exec(`INSERT INTO jobs(id, filename, file_type, owner_id, status_code) VALUES ('job-sync', 'file.mp3', 'audio', 'u1', 50)`)
	_ = IndexJobSearchContent("job-sync", "u1", "비밀 문서 내용")

	// Initially searchable by u1
	ids, err := SearchJobIDsByContent("u1", "비밀")
	if err != nil || len(ids) != 1 {
		t.Fatalf("expected searchable by u1, got %v, err=%v", ids, err)
	}

	// Update job owner to u2 via SaveJob
	job := &model.Job{
		Filename:   "file.mp3",
		FileType:   "audio",
		OwnerID:    "u2",
		StatusCode: 50,
	}
	if err := SaveJob("job-sync", job); err != nil {
		t.Fatalf("SaveJob failed: %v", err)
	}

	// Now should be searchable by u2 and not by u1
	idsU1, _ := SearchJobIDsByContent("u1", "비밀")
	if len(idsU1) != 0 {
		t.Fatalf("expected u1 not to find job after ownership transfer, got %v", idsU1)
	}
	idsU2, _ := SearchJobIDsByContent("u2", "비밀")
	if len(idsU2) != 1 || idsU2[0] != "job-sync" {
		t.Fatalf("expected u2 to find job after ownership transfer, got %v", idsU2)
	}
}

func TestCheckpointWAL(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".run"), 0o755); err != nil {
		t.Fatalf("mkdir .run: %v", err)
	}
	if err := Init(projectRoot); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if err := CheckpointWAL(nil); err != nil {
		t.Fatalf("CheckpointWAL failed: %v", err)
	}
}
