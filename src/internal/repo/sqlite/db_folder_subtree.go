package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"whisperserver/src/internal/domain"
)

// CollectFolderSubtreeIDs gathers the target folder and all its descendant folder IDs recursively.
func CollectFolderSubtreeIDs(ownerID, folderID string) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("no database connection available")
	}

	rows, err := dbConn.Query(`SELECT id, parent_id FROM folders WHERE owner_id = ?`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("query folders failed: %w", err)
	}
	defer rows.Close()

	childrenMap := make(map[string][]string)
	for rows.Next() {
		var id string
		var parentID sql.NullString
		if err := rows.Scan(&id, &parentID); err != nil {
			return nil, err
		}
		if parentID.Valid && parentID.String != "" {
			childrenMap[parentID.String] = append(childrenMap[parentID.String], id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	visited := make(map[string]bool)
	var result []string
	var queue []string
	queue = append(queue, folderID)

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if visited[curr] {
			continue
		}
		visited[curr] = true
		result = append(result, curr)
		for _, childID := range childrenMap[curr] {
			if !visited[childID] {
				queue = append(queue, childID)
			}
		}
	}
	return result, nil
}

// TrashFolderSubtreeTx performs atomic cascading trash for a folder subtree, its jobs, and active tasks.
func TrashFolderSubtreeTx(ctx context.Context, tx domain.Tx, ownerID, folderID string) error {
	exec := getExecutor(tx)
	if exec == nil {
		return fmt.Errorf("no database connection available")
	}

	// 1. Verify ownership
	var exists int
	err := exec.QueryRowContext(ctx, `SELECT COUNT(1) FROM folders WHERE id = ? AND owner_id = ?`, folderID, ownerID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("verify folder ownership failed: %w", err)
	}
	if exists == 0 {
		return sql.ErrNoRows
	}

	// 2. Collect subtree
	subtreeIDs, err := CollectFolderSubtreeIDs(ownerID, folderID)
	if err != nil {
		return fmt.Errorf("collect subtree failed: %w", err)
	}

	placeholders := make([]string, len(subtreeIDs))
	args := make([]any, len(subtreeIDs)+1)
	args[0] = ownerID
	for i, id := range subtreeIDs {
		placeholders[i] = "?"
		args[i+1] = id
	}
	inClause := strings.Join(placeholders, ",")

	nowTS := float64(time.Now().Unix())

	// 3. Update folders is_trashed = 1
	folderQuery := fmt.Sprintf(`UPDATE folders SET is_trashed = 1 WHERE owner_id = ? AND id IN (%s)`, inClause)
	if _, err := exec.ExecContext(ctx, folderQuery, args...); err != nil {
		return fmt.Errorf("update folders trashed failed: %w", err)
	}

	// 4. Update jobs is_trashed = 1
	jobArgs := append([]any{nowTS, ownerID}, args[1:]...)
	jobQuery := fmt.Sprintf(`UPDATE jobs SET is_trashed = 1, deleted_ts = ? WHERE owner_id = ? AND folder_id IN (%s)`, inClause)
	if _, err := exec.ExecContext(ctx, jobQuery, jobArgs...); err != nil {
		return fmt.Errorf("update jobs trashed failed: %w", err)
	}

	// 5. Cancel active tasks for those jobs
	taskQuery := fmt.Sprintf(`
		UPDATE tasks
		SET status = 'dead', lease_until = NULL
		WHERE owner_id = ? AND job_id IN (
			SELECT id FROM jobs WHERE owner_id = ? AND folder_id IN (%s)
		) AND status IN ('ready', 'leased')
	`, inClause)
	taskArgs := append([]any{ownerID, ownerID}, args[1:]...)
	if _, err := exec.ExecContext(ctx, taskQuery, taskArgs...); err != nil {
		return fmt.Errorf("cancel tasks failed: %w", err)
	}

	// 6. Record outbox event
	payload, _ := json.Marshal(map[string]any{
		"action":     "trash",
		"folder_id":  folderID,
		"subtree_ids": subtreeIDs,
	})
	_, _ = InsertOutboxEvent(ctx, tx, domain.DomainEvent{
		OwnerID:     ownerID,
		EventType:   domain.EventTypeFolderChanged,
		PayloadJSON: string(payload),
	})

	return nil
}

// RestoreFolderSubtreeTx atomically restores a folder subtree and its contained jobs.
func RestoreFolderSubtreeTx(ctx context.Context, tx domain.Tx, ownerID, folderID string) error {
	exec := getExecutor(tx)
	if exec == nil {
		return fmt.Errorf("no database connection available")
	}

	// 1. Verify ownership and load parent
	var parentID sql.NullString
	err := exec.QueryRowContext(ctx, `SELECT parent_id FROM folders WHERE id = ? AND owner_id = ?`, folderID, ownerID).Scan(&parentID)
	if err != nil {
		return fmt.Errorf("verify folder ownership failed: %w", err)
	}

	// If parent is trashed or missing, reset parent_id to root (NULL)
	if parentID.Valid && parentID.String != "" {
		var parentTrashed int
		err := exec.QueryRowContext(ctx, `SELECT is_trashed FROM folders WHERE id = ? AND owner_id = ?`, parentID.String, ownerID).Scan(&parentTrashed)
		if err != nil || parentTrashed == 1 {
			_, _ = exec.ExecContext(ctx, `UPDATE folders SET parent_id = NULL WHERE id = ? AND owner_id = ?`, folderID, ownerID)
		}
	}

	subtreeIDs, err := CollectFolderSubtreeIDs(ownerID, folderID)
	if err != nil {
		return fmt.Errorf("collect subtree failed: %w", err)
	}

	placeholders := make([]string, len(subtreeIDs))
	args := make([]any, len(subtreeIDs)+1)
	args[0] = ownerID
	for i, id := range subtreeIDs {
		placeholders[i] = "?"
		args[i+1] = id
	}
	inClause := strings.Join(placeholders, ",")

	// 2. Restore folders
	folderQuery := fmt.Sprintf(`UPDATE folders SET is_trashed = 0 WHERE owner_id = ? AND id IN (%s)`, inClause)
	if _, err := exec.ExecContext(ctx, folderQuery, args...); err != nil {
		return fmt.Errorf("restore folders failed: %w", err)
	}

	// 3. Restore jobs
	jobQuery := fmt.Sprintf(`UPDATE jobs SET is_trashed = 0, deleted_ts = 0 WHERE owner_id = ? AND folder_id IN (%s)`, inClause)
	if _, err := exec.ExecContext(ctx, jobQuery, args...); err != nil {
		return fmt.Errorf("restore jobs failed: %w", err)
	}

	// 4. Record outbox event
	payload, _ := json.Marshal(map[string]any{
		"action":     "restore",
		"folder_id":  folderID,
		"subtree_ids": subtreeIDs,
	})
	_, _ = InsertOutboxEvent(ctx, tx, domain.DomainEvent{
		OwnerID:     ownerID,
		EventType:   domain.EventTypeFolderChanged,
		PayloadJSON: string(payload),
	})

	return nil
}
