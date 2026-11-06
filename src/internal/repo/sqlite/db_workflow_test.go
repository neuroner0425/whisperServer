package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"
	"whisperserver/src/internal/domain"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	projectRoot := t.TempDir()
	if err := Init(projectRoot); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	t.Cleanup(func() {
		Close()
	})
}

func createTestUserAndJob(t *testing.T, userID, jobID string) {
	t.Helper()
	_, err := dbConn.Exec(`INSERT INTO users (id, email, password_hash) VALUES (?, ?, 'hash')`, userID, userID+"@example.com")
	if err != nil {
		t.Fatalf("insert user failed: %v", err)
	}
	_, err = dbConn.Exec(`INSERT INTO jobs (id, owner_id, status_code, filename) VALUES (?, ?, 10, 'test.mp3')`, jobID, userID)
	if err != nil {
		t.Fatalf("insert job failed: %v", err)
	}
}

func TestWorkflowSchemaMigration(t *testing.T) {
	setupTestDB(t)

	// Verify tables exist
	tables := []string{"tasks", "job_steps", "outbox", "user_settings"}
	for _, tbl := range tables {
		var name string
		err := dbConn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name)
		if err != nil || name != tbl {
			t.Fatalf("expected table %s to exist, err: %v", tbl, err)
		}
	}

	// Verify jobs expanded columns
	hasParams, err := columnExists(dbConn, "jobs", "params_json")
	if err != nil || !hasParams {
		t.Fatalf("expected jobs.params_json to exist, err: %v", err)
	}
	hasVersion, err := columnExists(dbConn, "jobs", "version")
	if err != nil || !hasVersion {
		t.Fatalf("expected jobs.version to exist, err: %v", err)
	}
}

func TestTasksQueue_ClaimAndFencingToken(t *testing.T) {
	setupTestDB(t)
	ctx := context.Background()

	createTestUserAndJob(t, "user1", "job1")

	// 1. Enqueue task
	task := domain.Task{
		ID:            "task_001",
		JobID:         "job1",
		OwnerID:       "user1",
		Step:          domain.StepAVScan,
		ResourceClass: domain.ResourceClassMedia,
		Priority:      10,
		PayloadJSON:   `{"file":"raw"}`,
		MaxAttempts:   3,
	}
	if err := EnqueueTask(ctx, nil, task); err != nil {
		t.Fatalf("EnqueueTask failed: %v", err)
	}

	// 2. Claim task by Worker A
	workerA := "worker_A"
	claimedA, err := ClaimTask(ctx, workerA, []domain.ResourceClass{domain.ResourceClassMedia}, 1*time.Second)
	if err != nil {
		t.Fatalf("ClaimTask by workerA failed: %v", err)
	}
	if claimedA == nil || claimedA.ID != "task_001" {
		t.Fatalf("expected task_001 claimed by workerA, got %+v", claimedA)
	}
	if claimedA.Attempt != 1 {
		t.Fatalf("expected attempt 1, got %d", claimedA.Attempt)
	}

	// 3. Heartbeat by Worker A
	if err := HeartbeatTask(ctx, claimedA.ID, workerA, 2*time.Second); err != nil {
		t.Fatalf("Heartbeat by workerA failed: %v", err)
	}

	// 4. Invalid worker heartbeat should fail
	if err := HeartbeatTask(ctx, claimedA.ID, "worker_fake", 2*time.Second); !errors.Is(err, ErrTaskNotLeased) {
		t.Fatalf("expected ErrTaskNotLeased for fake worker, got %v", err)
	}

	// 5. Simulate Lease Expiration: force lease_until into the past
	_, err = dbConn.Exec(`UPDATE tasks SET lease_until = datetime('now', '-10 seconds') WHERE id = ?`, claimedA.ID)
	if err != nil {
		t.Fatalf("force lease expiration failed: %v", err)
	}

	// 6. Claim expired task by Worker B (Self-healing lease recovery)
	workerB := "worker_B"
	claimedB, err := ClaimTask(ctx, workerB, []domain.ResourceClass{domain.ResourceClassMedia}, 5*time.Second)
	if err != nil {
		t.Fatalf("ClaimTask by workerB failed: %v", err)
	}
	if claimedB == nil || claimedB.ID != "task_001" {
		t.Fatalf("expected workerB to claim expired task_001, got %+v", claimedB)
	}
	if claimedB.Attempt != 2 {
		t.Fatalf("expected attempt 2 for workerB, got %d", claimedB.Attempt)
	}

	// 7. Zombie Worker A tries to complete task with stale fencing token (attempt 1) -> must fail!
	err = CompleteTask(ctx, nil, claimedA.ID, workerA, claimedA.Attempt)
	if !errors.Is(err, ErrFencingTokenMismatch) {
		t.Fatalf("expected ErrFencingTokenMismatch for zombie workerA, got %v", err)
	}

	// 8. Legitimate Worker B completes task with valid fencing token (attempt 2) -> must succeed!
	err = CompleteTask(ctx, nil, claimedB.ID, workerB, claimedB.Attempt)
	if err != nil {
		t.Fatalf("expected workerB CompleteTask to succeed, got %v", err)
	}

	// 9. Verify task status is 'done'
	completedTask, err := GetTask(ctx, "task_001")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if completedTask.Status != domain.TaskStatusDone {
		t.Fatalf("expected task status done, got %s", completedTask.Status)
	}
}

func TestTasksQueue_RetryAndDeadLetter(t *testing.T) {
	setupTestDB(t)
	ctx := context.Background()

	createTestUserAndJob(t, "user1", "job2")

	task := domain.Task{
		ID:            "task_retry",
		JobID:         "job2",
		OwnerID:       "user1",
		Step:          domain.StepTranscribe,
		ResourceClass: domain.ResourceClassSTT,
		MaxAttempts:   2,
	}
	if err := EnqueueTask(ctx, nil, task); err != nil {
		t.Fatalf("EnqueueTask failed: %v", err)
	}

	// Attempt 1 Claim
	worker := "stt_worker_1"
	claimed1, err := ClaimTask(ctx, worker, []domain.ResourceClass{domain.ResourceClassSTT}, 5*time.Second)
	if err != nil || claimed1 == nil {
		t.Fatalf("Claim 1 failed: %v, task=%+v", err, claimed1)
	}

	// Fail Attempt 1 with retry
	retryDur := 10 * time.Millisecond
	err = FailTask(ctx, nil, claimed1.ID, worker, claimed1.Attempt, &retryDur, "whisper out of memory")
	if err != nil {
		t.Fatalf("FailTask 1 failed: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	// Attempt 2 Claim
	claimed2, err := ClaimTask(ctx, worker, []domain.ResourceClass{domain.ResourceClassSTT}, 5*time.Second)
	if err != nil || claimed2 == nil {
		t.Fatalf("Claim 2 failed: %v, task=%+v", err, claimed2)
	}
	if claimed2.Attempt != 2 {
		t.Fatalf("expected attempt 2, got %d", claimed2.Attempt)
	}

	// Fail Attempt 2 -> Should become DEAD because attempt >= maxAttempts (2 >= 2)
	err = FailTask(ctx, nil, claimed2.ID, worker, claimed2.Attempt, &retryDur, "whisper repeated crash")
	if err != nil {
		t.Fatalf("FailTask 2 failed: %v", err)
	}

	deadTask, err := GetTask(ctx, "task_retry")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if deadTask.Status != domain.TaskStatusDead {
		t.Fatalf("expected dead status, got %s", deadTask.Status)
	}
}

func TestJobStepsProvenance(t *testing.T) {
	setupTestDB(t)
	ctx := context.Background()

	createTestUserAndJob(t, "owner_u", "job_prov")
	createTestUserAndJob(t, "other_u", "job_other")

	now := time.Now().UTC()
	step := domain.JobStep{
		JobID:         "job_prov",
		Step:          domain.StepAVScan,
		Attempt:       1,
		Status:        domain.StepStatusRunning,
		Engine:        "clamav",
		StartedAt:     now,
		ParamsJSON:    `{"mode":"clamd"}`,
		QualityReport: `{"clean":true}`,
	}

	if err := RecordJobStep(ctx, nil, step); err != nil {
		t.Fatalf("RecordJobStep insert failed: %v", err)
	}

	// Update step to completed
	step.Status = domain.StepStatusCompleted
	finishedAt := now.Add(200 * time.Millisecond)
	step.FinishedAt = &finishedAt
	if err := RecordJobStep(ctx, nil, step); err != nil {
		t.Fatalf("RecordJobStep update failed: %v", err)
	}

	// Other user should be unauthorized
	_, err := ListJobSteps(ctx, "other_u", "job_prov")
	if err == nil {
		t.Fatalf("expected unauthorized error for other user")
	}

	// Owner should see recorded step
	steps, err := ListJobSteps(ctx, "owner_u", "job_prov")
	if err != nil {
		t.Fatalf("ListJobSteps failed: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Status != domain.StepStatusCompleted || steps[0].Engine != "clamav" {
		t.Fatalf("unexpected step data: %+v", steps[0])
	}
}

func TestOutbox_TransactionAndReplay(t *testing.T) {
	setupTestDB(t)
	ctx := context.Background()

	createTestUserAndJob(t, "user_out", "job_out")

	txMgr := NewTxManager(dbConn)
	tx, err := txMgr.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	evt := domain.DomainEvent{
		OwnerID:     "user_out",
		JobID:       "job_out",
		EventType:   domain.EventTypeJobStatus,
		PayloadJSON: `{"status_code":20}`,
	}

	seq1, err := InsertOutboxEvent(ctx, tx, evt)
	if err != nil {
		t.Fatalf("InsertOutboxEvent failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Fetch unpublished
	unpub, err := FetchUnpublishedOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("FetchUnpublishedOutbox failed: %v", err)
	}
	if len(unpub) != 1 || unpub[0].Seq != seq1 {
		t.Fatalf("expected seq %d, got %+v", seq1, unpub)
	}

	// Mark published
	if err := MarkOutboxPublished(ctx, []int64{seq1}); err != nil {
		t.Fatalf("MarkOutboxPublished failed: %v", err)
	}

	// Should be 0 unpublished now
	unpub2, err := FetchUnpublishedOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("FetchUnpublishedOutbox failed: %v", err)
	}
	if len(unpub2) != 0 {
		t.Fatalf("expected 0 unpublished events, got %d", len(unpub2))
	}

	// Test replay via FetchOutboxSince
	replayed, err := FetchOutboxSince(ctx, "user_out", seq1-1, 10)
	if err != nil {
		t.Fatalf("FetchOutboxSince failed: %v", err)
	}
	if len(replayed) != 1 || replayed[0].Seq != seq1 {
		t.Fatalf("expected replayed event %d, got %+v", seq1, replayed)
	}

	// Other owner replay should return empty
	otherReplayed, err := FetchOutboxSince(ctx, "other_user", seq1-1, 10)
	if err != nil {
		t.Fatalf("FetchOutboxSince other failed: %v", err)
	}
	if len(otherReplayed) != 0 {
		t.Fatalf("expected 0 events for other owner, got %d", len(otherReplayed))
	}
}

func TestUserSettings_Persistence(t *testing.T) {
	setupTestDB(t)
	ctx := context.Background()

	createTestUserAndJob(t, "user_pref", "job_pref")

	// Initially nil
	prefs, err := GetUserSettings(ctx, "user_pref")
	if err != nil {
		t.Fatalf("GetUserSettings failed: %v", err)
	}
	if prefs != nil {
		t.Fatalf("expected nil prefs initially, got %+v", prefs)
	}

	// Save
	toSave := domain.UserSettings{
		UserID:       "user_pref",
		LLMPrefsJSON: `{"polish":{"model":"gemini-2.5-flash-lite","thinking_level":"low"}}`,
	}
	if err := SaveUserSettings(ctx, toSave); err != nil {
		t.Fatalf("SaveUserSettings failed: %v", err)
	}

	saved, err := GetUserSettings(ctx, "user_pref")
	if err != nil {
		t.Fatalf("GetUserSettings after save failed: %v", err)
	}
	if saved == nil || saved.LLMPrefsJSON != toSave.LLMPrefsJSON {
		t.Fatalf("unexpected saved prefs: %+v", saved)
	}
}

