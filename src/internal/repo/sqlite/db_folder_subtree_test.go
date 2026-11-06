package sqlite

import (
	"context"
	"testing"
	"whisperserver/src/internal/domain"
)

func TestTrashAndRestoreFolderSubtreeTx(t *testing.T) {
	setupTestDB(t)
	ctx := context.Background()

	ownerID := "user_sub"
	rawDB := GetRawDBForTest()

	// 1. Setup user
	_, err := rawDB.Exec(`INSERT INTO users (id, email, password_hash) VALUES (?, 'sub@test.com', 'h')`, ownerID)
	if err != nil {
		t.Fatalf("insert user failed: %v", err)
	}

	// 2. Setup folder hierarchy: F1 -> F2 -> F3
	_, err = rawDB.Exec(`INSERT INTO folders (id, owner_id, name, parent_id) VALUES ('f1', ?, 'RootFolder', NULL)`, ownerID)
	if err != nil {
		t.Fatalf("insert f1 failed: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO folders (id, owner_id, name, parent_id) VALUES ('f2', ?, 'SubFolder', 'f1')`, ownerID)
	if err != nil {
		t.Fatalf("insert f2 failed: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO folders (id, owner_id, name, parent_id) VALUES ('f3', ?, 'LeafFolder', 'f2')`, ownerID)
	if err != nil {
		t.Fatalf("insert f3 failed: %v", err)
	}

	// 3. Setup jobs in folders
	_, err = rawDB.Exec(`INSERT INTO jobs (id, owner_id, status_code, filename, folder_id) VALUES ('j1', ?, 10, 'audio1.mp3', 'f1')`, ownerID)
	if err != nil {
		t.Fatalf("insert j1 failed: %v", err)
	}
	_, err = rawDB.Exec(`INSERT INTO jobs (id, owner_id, status_code, filename, folder_id) VALUES ('j2', ?, 20, 'audio2.mp3', 'f3')`, ownerID)
	if err != nil {
		t.Fatalf("insert j2 failed: %v", err)
	}

	// 4. Setup active task for j2
	task := domain.Task{
		ID:            "task_sub_j2",
		JobID:         "j2",
		OwnerID:       ownerID,
		Step:          domain.StepTranscribe,
		ResourceClass: domain.ResourceClassSTT,
		Status:        domain.TaskStatusReady,
	}
	if err := EnqueueTask(ctx, nil, task); err != nil {
		t.Fatalf("enqueue task failed: %v", err)
	}

	// 5. Execute TrashFolderSubtreeTx
	txMgr := NewTxManager(nil)
	tx, err := txMgr.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := TrashFolderSubtreeTx(ctx, tx, ownerID, "f1"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("TrashFolderSubtreeTx failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify all folders F1, F2, F3 are trashed
	var trashedCount int
	err = rawDB.QueryRow(`SELECT COUNT(1) FROM folders WHERE owner_id = ? AND is_trashed = 1`, ownerID).Scan(&trashedCount)
	if err != nil || trashedCount != 3 {
		t.Fatalf("expected 3 trashed folders, got %d (err: %v)", trashedCount, err)
	}

	// Verify jobs j1, j2 are trashed
	var trashedJobs int
	err = rawDB.QueryRow(`SELECT COUNT(1) FROM jobs WHERE owner_id = ? AND is_trashed = 1`, ownerID).Scan(&trashedJobs)
	if err != nil || trashedJobs != 2 {
		t.Fatalf("expected 2 trashed jobs, got %d (err: %v)", trashedJobs, err)
	}

	// Verify task_sub_j2 is dead
	taskRec, err := GetTask(ctx, "task_sub_j2")
	if err != nil || taskRec.Status != domain.TaskStatusDead {
		t.Fatalf("expected dead task, got status=%s err=%v", taskRec.Status, err)
	}

	// 6. Execute RestoreFolderSubtreeTx
	txRestore, err := txMgr.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx for restore failed: %v", err)
	}
	if err := RestoreFolderSubtreeTx(ctx, txRestore, ownerID, "f1"); err != nil {
		_ = txRestore.Rollback()
		t.Fatalf("RestoreFolderSubtreeTx failed: %v", err)
	}
	if err := txRestore.Commit(); err != nil {
		t.Fatalf("Commit restore failed: %v", err)
	}

	// Verify restored: is_trashed = 0
	err = rawDB.QueryRow(`SELECT COUNT(1) FROM folders WHERE owner_id = ? AND is_trashed = 0`, ownerID).Scan(&trashedCount)
	if err != nil || trashedCount != 3 {
		t.Fatalf("expected 3 restored folders, got %d (err: %v)", trashedCount, err)
	}
	err = rawDB.QueryRow(`SELECT COUNT(1) FROM jobs WHERE owner_id = ? AND is_trashed = 0`, ownerID).Scan(&trashedJobs)
	if err != nil || trashedJobs != 2 {
		t.Fatalf("expected 2 restored jobs, got %d (err: %v)", trashedJobs, err)
	}
}

