package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	model "whisperserver/src/internal/domain"
)

func TestCollectFolderSubtree(t *testing.T) {
	rt := New(Config{
		TmpFolder: t.TempDir(),
		ListAllFoldersByOwner: func(userID string, trashed bool) ([]model.Folder, error) {
			return []model.Folder{
				{ID: "root", ParentID: ""},
				{ID: "child", ParentID: "root"},
				{ID: "grand", ParentID: "child"},
			}, nil
		},
		GetFolderByID: func(userID, folderID string) (*model.Folder, error) {
			return &model.Folder{ID: folderID, ParentID: map[string]string{"root": "", "child": "root", "grand": "child"}[folderID]}, nil
		},
		SetFolderTrashed: func(userID, folderID string, trashed bool) error { return nil },
	})
	subtree := rt.CollectFolderSubtree("u1", []string{"root"}, false)
	for _, id := range []string{"root", "child", "grand"} {
		if _, ok := subtree[id]; !ok {
			t.Fatalf("missing subtree id %s", id)
		}
	}
}

func TestCleanupInactiveTempWavs(t *testing.T) {
	dir := t.TempDir()
	rt := New(Config{TmpFolder: dir})
	for _, name := range []string{"a.wav", "b.m4a", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rt.CleanupInactiveTempWavs()
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatalf("keep.txt should remain: %v", err)
	}
	for _, name := range []string{"a.wav", "b.m4a"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed", name)
		}
	}
}

func TestRuntimeJobPersistenceSeparation(t *testing.T) {
	savedJobs := map[string]*model.Job{}
	saveJobCalls := 0
	blobCalls := 0

	rt := New(Config{
		TmpFolder: t.TempDir(),
		SaveJob: func(id string, job *model.Job) error {
			saveJobCalls++
			savedJobs[id] = job.Clone()
			return nil
		},
		SaveJobBlob: func(id, kind string, data []byte) error {
			blobCalls++
			return nil
		},
	})

	// 1. AddJob should persist via SaveJob.
	rt.AddJob("job-1", &model.Job{
		StatusCode: 10,
		Filename:   "test.wav",
		OwnerID:    "user-1",
	})
	if saveJobCalls != 1 {
		t.Fatalf("expected 1 SaveJob call on AddJob, got %d", saveJobCalls)
	}

	// 2. Volatile updates (progress_percent, phase, progress_label, preview_text) MUST NOT call SaveJob.
	rt.SetJobFields("job-1", map[string]any{
		"phase":            "전사 중",
		"progress_percent": 45,
		"progress_label":   "진행 중...",
	})
	if saveJobCalls != 1 {
		t.Fatalf("expected SaveJob to NOT be called for volatile progress fields, got %d", saveJobCalls)
	}

	// Verify in-memory state updated.
	job := rt.GetJob("job-1")
	if job == nil || job.ProgressPercent != 45 || job.Phase != "전사 중" {
		t.Fatalf("in-memory job should have updated progress: %+v", job)
	}

	// 3. Preview line and text appends MUST NOT call SaveJobBlob.
	rt.AppendJobPreviewLine("job-1", "첫 번째 줄")
	rt.AppendJobPreviewLine("job-1", "두 번째 줄")
	rt.ReplaceJobPreviewText("job-1", "전체 교체 텍스트")
	if blobCalls != 0 {
		t.Fatalf("expected 0 SaveJobBlob calls for preview text, got %d", blobCalls)
	}
	job = rt.GetJob("job-1")
	if job.PreviewText != "전체 교체 텍스트" {
		t.Fatalf("expected updated in-memory preview text, got %q", job.PreviewText)
	}

	// 4. Lifecycle state transition (status = Completed, progress = 100) MUST call SaveJob.
	rt.SetJobFields("job-1", map[string]any{
		"status":           "완료",
		"status_code":      50,
		"completed_at":     "2026-09-11 22:00:00",
		"progress_percent": 100,
	})
	if saveJobCalls != 2 {
		t.Fatalf("expected SaveJob to be called on status completion, got %d", saveJobCalls)
	}
	saved := savedJobs["job-1"]
	if saved == nil || saved.StatusCode != 50 || saved.ProgressPercent != 100 {
		t.Fatalf("expected saved job with status 50 and percent 100, got %+v", saved)
	}
}

func TestRuntimeJobDeletion(t *testing.T) {
	deletedIDs := []string{}
	rt := New(Config{
		TmpFolder: t.TempDir(),
		SaveJob: func(id string, job *model.Job) error {
			return nil
		},
		DeleteJobs: func(ids []string) error {
			deletedIDs = append(deletedIDs, ids...)
			return nil
		},
	})

	rt.AddJob("job-1", &model.Job{StatusCode: 10, OwnerID: "user-1"})
	rt.AddJob("job-2", &model.Job{StatusCode: 10, OwnerID: "user-1"})

	rt.DeleteJobs([]string{"job-1"})
	if len(deletedIDs) != 1 || deletedIDs[0] != "job-1" {
		t.Fatalf("expected job-1 to be deleted via DeleteJobs, got %v", deletedIDs)
	}
	if rt.GetJob("job-1") != nil {
		t.Fatalf("job-1 should be removed from in-memory snapshot")
	}
	if rt.GetJob("job-2") == nil {
		t.Fatalf("job-2 should remain in in-memory snapshot")
	}
}

func TestCoordinatorMethods(t *testing.T) {
	saveJobCalls := 0
	savedJobs := map[string]*model.Job{}

	rt := New(Config{
		TmpFolder: t.TempDir(),
		SaveJob: func(id string, job *model.Job) error {
			saveJobCalls++
			savedJobs[id] = job.Clone()
			return nil
		},
		GetJobByID: func(id string) (*model.Job, error) {
			return savedJobs[id], nil
		},
	})

	sub := rt.Broker().Subscribe("user-1")
	defer rt.Broker().Unsubscribe("user-1", sub)

	// Register job
	rt.AddJob("job-1", &model.Job{
		StatusCode: model.JobStatusPendingCode,
		Filename:   "lecture.mp3",
		OwnerID:    "user-1",
	})
	if saveJobCalls != 1 {
		t.Fatalf("expected 1 SaveJob call on AddJob, got %d", saveJobCalls)
	}

	// UpdateProgress should NOT hit SaveJob
	rt.UpdateProgress("job-1", "음성 전사 중", 65, "65% 완료")
	if saveJobCalls != 1 {
		t.Fatalf("expected SaveJob NOT called on UpdateProgress, got %d", saveJobCalls)
	}

	job := rt.GetJob("job-1")
	if job.Phase != "음성 전사 중" || job.ProgressPercent != 65 || job.ProgressLabel != "65% 완료" {
		t.Fatalf("unexpected progress fields: %+v", job)
	}

	// Verify ActiveJobsSnapshot includes job-1
	activeMap := rt.ActiveJobsSnapshot()
	if activeMap["job-1"] == nil {
		t.Fatalf("job-1 should be in ActiveJobsSnapshot")
	}

	// TransitionStatus to Completed should hit SaveJob and evict from ActiveJobs
	rt.TransitionStatus("job-1", "완료", model.JobStatusCompletedCode, map[string]any{
		"progress_percent": 100,
		"phase":            "완료",
	})
	if saveJobCalls != 2 {
		t.Fatalf("expected SaveJob to be called on TransitionStatus, got %d", saveJobCalls)
	}

	// In-memory active cache should no longer contain job-1
	if rt.ActiveJobsSnapshot()["job-1"] != nil {
		t.Fatalf("completed job-1 should be evicted from ActiveJobsSnapshot")
	}

	// GetJob should still succeed via GetJobByID fallback
	job = rt.GetJob("job-1")
	if job == nil || job.StatusCode != model.JobStatusCompletedCode || job.Status != "완료" {
		t.Fatalf("unexpected status after transition: %+v", job)
	}

	// Verify SSE messages were received
	received := 0
	for {
		select {
		case <-sub:
			received++
		default:
			goto done
		}
	}
done:
	if received < 2 {
		t.Fatalf("expected at least 2 SSE events, got %d", received)
	}
}

func TestPhase32StateStoreEvictionAndFallback(t *testing.T) {
	dbJobs := map[string]*model.Job{
		"comp-1": {
			StatusCode: model.JobStatusCompletedCode,
			Status:     "완료",
			Filename:   "original.mp3",
			OwnerID:    "user-1",
			FolderID:   "f1",
		},
	}
	var trashedFolders []string

	rt := New(Config{
		TmpFolder: t.TempDir(),
		SaveJob: func(id string, j *model.Job) error {
			dbJobs[id] = j.Clone()
			return nil
		},
		GetJobByID: func(id string) (*model.Job, error) {
			return dbJobs[id], nil
		},
		MarkJobsTrashedByFolderIDs: func(ownerID string, folderIDs []string, deletedTS float64) error {
			trashedFolders = append(trashedFolders, folderIDs...)
			return nil
		},
	})

	// 1. In-memory active cache should be empty
	if len(rt.ActiveJobsSnapshot()) != 0 {
		t.Fatalf("expected 0 active jobs initially, got %d", len(rt.ActiveJobsSnapshot()))
	}

	// 2. GetJob on completed job should resolve via DB fallback
	job := rt.GetJob("comp-1")
	if job == nil || job.Filename != "original.mp3" || job.StatusCode != model.JobStatusCompletedCode {
		t.Fatalf("expected job to be resolved via DB fallback: %+v", job)
	}

	// 3. Mutating metadata on completed job (not in RAM) should update DB and remain evicted
	rt.MutateMetadata("comp-1", map[string]any{"filename": "renamed.mp3"})
	if dbJobs["comp-1"].Filename != "renamed.mp3" {
		t.Fatalf("expected db to have renamed filename, got %s", dbJobs["comp-1"].Filename)
	}
	if rt.ActiveJobsSnapshot()["comp-1"] != nil {
		t.Fatalf("completed job should not be kept in active jobs snapshot after metadata mutation")
	}

	// 4. MarkSubtreeJobsTrashed should invoke MarkJobsTrashedByFolderIDs
	rt.MarkSubtreeJobsTrashed("user-1", map[string]struct{}{"f1": {}})
	if len(trashedFolders) != 1 || trashedFolders[0] != "f1" {
		t.Fatalf("expected MarkJobsTrashedByFolderIDs called with f1, got %v", trashedFolders)
	}
}

func TestDeleteCompletedJobEmitsNotification(t *testing.T) {
	notifiedOwners := []string{}
	deletedDBIDs := []string{}

	rt := New(Config{
		TmpFolder: t.TempDir(),
		GetOwnerIDsByJobIDs: func(ids []string) ([]string, error) {
			return []string{"user-completed"}, nil
		},
		DeleteJobs: func(ids []string) error {
			deletedDBIDs = append(deletedDBIDs, ids...)
			return nil
		},
	})

	// Listen to notifications via broker
	ch := rt.Broker().Subscribe("user-completed")
	defer rt.Broker().Unsubscribe("user-completed", ch)

	// Delete a completed job that is NOT in RAM
	rt.DeleteJobs([]string{"job-completed-1"})

	if len(deletedDBIDs) != 1 || deletedDBIDs[0] != "job-completed-1" {
		t.Fatalf("expected job-completed-1 deleted from DB, got %v", deletedDBIDs)
	}

	select {
	case msg := <-ch:
		if !strings.Contains(string(msg), `"type":"files.changed"`) {
			t.Fatalf("expected files.changed event payload, got %s", string(msg))
		}
		notifiedOwners = append(notifiedOwners, "user-completed")
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for files.changed SSE notification on completed job deletion")
	}

	if len(notifiedOwners) != 1 {
		t.Fatalf("expected 1 notification received, got %d", len(notifiedOwners))
	}
}

func TestCompletedJobLRUCache(t *testing.T) {
	dbCalls := 0
	dbJobs := map[string]*model.Job{
		"comp-lru": {
			StatusCode: model.JobStatusCompletedCode,
			Filename:   "cached.mp3",
			OwnerID:    "user-lru",
		},
	}

	rt := New(Config{
		TmpFolder: t.TempDir(),
		GetJobByID: func(id string) (*model.Job, error) {
			dbCalls++
			return dbJobs[id], nil
		},
		SaveJob: func(id string, j *model.Job) error {
			dbJobs[id] = j.Clone()
			return nil
		},
	})

	// First call: hits DB
	j1 := rt.GetJob("comp-lru")
	if j1 == nil || j1.Filename != "cached.mp3" {
		t.Fatalf("expected job from DB")
	}
	if dbCalls != 1 {
		t.Fatalf("expected 1 DB call, got %d", dbCalls)
	}

	// Second call: should hit LRU cache (no extra DB call!)
	j2 := rt.GetJob("comp-lru")
	if j2 == nil || j2.Filename != "cached.mp3" {
		t.Fatalf("expected job from LRU cache")
	}
	if dbCalls != 1 {
		t.Fatalf("expected still 1 DB call due to LRU cache, got %d", dbCalls)
	}

	// Mutate metadata: should invalidate LRU and update
	rt.MutateMetadata("comp-lru", map[string]any{"filename": "cached-new.mp3"})

	// Third call after mutation: retrieves updated version
	j3 := rt.GetJob("comp-lru")
	if j3 == nil || j3.Filename != "cached-new.mp3" {
		t.Fatalf("expected updated filename, got %v", j3)
	}
}
