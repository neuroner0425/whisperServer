package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"whisperserver/src/internal/domain"
)

const sqliteTimeFormat = "2006-01-02 15:04:05"

func formatSQLiteTime(t time.Time) string {
	return t.UTC().Format(sqliteTimeFormat)
}

func parseSQLiteTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	// Try standard SQLite format
	if t, err := time.Parse(sqliteTimeFormat, s); err == nil {
		return t.UTC(), nil
	}
	// Try RFC3339
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	// Fallback RFC3339Nano
	return time.Parse(time.RFC3339Nano, s)
}

// EnqueueTask adds a task into the tasks queue within an optional transaction.
func EnqueueTask(ctx context.Context, tx domain.Tx, task domain.Task) error {
	exec := getExecutor(tx)
	if exec == nil {
		return fmt.Errorf("no database connection available")
	}

	if task.ID == "" {
		task.ID = fmt.Sprintf("task_%d", time.Now().UnixNano())
	}
	if task.Status == "" {
		task.Status = domain.TaskStatusReady
	}
	if task.MaxAttempts <= 0 {
		task.MaxAttempts = 5
	}
	if task.RunAfter.IsZero() {
		task.RunAfter = time.Now().UTC()
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}

	query := `
		INSERT INTO tasks (
			id, job_id, owner_id, step, resource_class, priority,
			payload_json, status, lease_owner, lease_until, attempt,
			max_attempts, run_after, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	var leaseUntilStr *string
	if task.LeaseUntil != nil {
		s := formatSQLiteTime(*task.LeaseUntil)
		leaseUntilStr = &s
	}

	_, err := exec.ExecContext(ctx, query,
		task.ID,
		task.JobID,
		task.OwnerID,
		string(task.Step),
		string(task.ResourceClass),
		task.Priority,
		task.PayloadJSON,
		string(task.Status),
		task.LeaseOwner,
		leaseUntilStr,
		task.Attempt,
		task.MaxAttempts,
		formatSQLiteTime(task.RunAfter),
		formatSQLiteTime(task.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("EnqueueTask insert failed: %w", err)
	}
	return nil
}

// ClaimTask attempts to claim an eligible task for the specified workerID and resource classes.
// It detects expired leases (lease_until < now) and transitions the task to leased with a fencing increment.
func ClaimTask(ctx context.Context, workerID string, classes []domain.ResourceClass, leaseDur time.Duration) (*domain.Task, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("no database connection available")
	}
	if len(classes) == 0 {
		return nil, nil
	}

	// Build IN clause for resource classes
	placeholders := make([]string, len(classes))
	args := make([]any, len(classes))
	for i, c := range classes {
		placeholders[i] = "?"
		args[i] = string(c)
	}
	inClause := strings.Join(placeholders, ",")

	now := time.Now().UTC()
	nowStr := formatSQLiteTime(now)
	leaseUntil := now.Add(leaseDur)
	leaseUntilStr := formatSQLiteTime(leaseUntil)

	// SQLite claim query using BEGIN IMMEDIATE to avoid lock escalation deadlocks.
	conn, err := dbConn.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("ClaimTask get db conn failed: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("ClaimTask BEGIN IMMEDIATE failed: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	selectQuery := fmt.Sprintf(`
		SELECT id, job_id, owner_id, step, resource_class, priority,
		       payload_json, status, lease_owner, lease_until, attempt,
		       max_attempts, run_after, created_at
		FROM tasks
		WHERE (status = 'ready' OR (status = 'leased' AND lease_until < ?))
		  AND resource_class IN (%s)
		  AND run_after <= ?
		ORDER BY priority DESC, created_at ASC
		LIMIT 1
	`, inClause)

	queryArgs := append([]any{nowStr}, args...)
	queryArgs = append(queryArgs, nowStr)

	row := conn.QueryRowContext(ctx, selectQuery, queryArgs...)

	var (
		t             domain.Task
		stepStr       string
		classStr      string
		statusStr     string
		leaseOwner    sql.NullString
		leaseUntilRaw sql.NullString
		runAfterRaw   string
		createdAtRaw  string
	)

	err = row.Scan(
		&t.ID,
		&t.JobID,
		&t.OwnerID,
		&stepStr,
		&classStr,
		&t.Priority,
		&t.PayloadJSON,
		&statusStr,
		&leaseOwner,
		&leaseUntilRaw,
		&t.Attempt,
		&t.MaxAttempts,
		&runAfterRaw,
		&createdAtRaw,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("ClaimTask scan candidate failed: %w", err)
	}

	t.Step = domain.StepType(stepStr)
	t.ResourceClass = domain.ResourceClass(classStr)
	t.Status = domain.TaskStatus(statusStr)
	if t.RunAfter, err = parseSQLiteTime(runAfterRaw); err != nil {
		t.RunAfter = now
	}
	if t.CreatedAt, err = parseSQLiteTime(createdAtRaw); err != nil {
		t.CreatedAt = now
	}

	// Increment attempt (fencing token) and update lease
	newAttempt := t.Attempt + 1
	updateQuery := `
		UPDATE tasks
		SET status = 'leased',
		    lease_owner = ?,
		    lease_until = ?,
		    attempt = ?
		WHERE id = ?
		  AND (status = 'ready' OR (status = 'leased' AND lease_until < ?))
	`
	res, err := conn.ExecContext(ctx, updateQuery, workerID, leaseUntilStr, newAttempt, t.ID, nowStr)
	if err != nil {
		return nil, fmt.Errorf("ClaimTask update lease failed: %w", err)
	}
	rowsAff, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rowsAff == 0 {
		// Another worker claimed it concurrently
		return nil, nil
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, err
	}
	committed = true

	t.Status = domain.TaskStatusLeased
	t.LeaseOwner = &workerID
	t.LeaseUntil = &leaseUntil
	t.Attempt = newAttempt

	return &t, nil
}

// HeartbeatTask extends the lease for an active task if the caller holds the valid lease.
func HeartbeatTask(ctx context.Context, taskID, workerID string, leaseDur time.Duration) error {
	if dbConn == nil {
		return fmt.Errorf("no database connection available")
	}

	newLeaseUntil := time.Now().UTC().Add(leaseDur)
	query := `
		UPDATE tasks
		SET lease_until = ?
		WHERE id = ? AND lease_owner = ? AND status = 'leased'
	`
	res, err := dbConn.ExecContext(ctx, query, formatSQLiteTime(newLeaseUntil), taskID, workerID)
	if err != nil {
		return fmt.Errorf("HeartbeatTask failed: %w", err)
	}
	rowsAff, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAff == 0 {
		return ErrTaskNotLeased
	}
	return nil
}

// CompleteTask marks a task done in the database, verifying the fencing token (workerID & attempt).
func CompleteTask(ctx context.Context, tx domain.Tx, taskID, workerID string, attempt int) error {
	exec := getExecutor(tx)
	if exec == nil {
		return fmt.Errorf("no database connection available")
	}

	query := `
		UPDATE tasks
		SET status = 'done', lease_until = NULL
		WHERE id = ? AND lease_owner = ? AND attempt = ? AND status = 'leased'
	`
	res, err := exec.ExecContext(ctx, query, taskID, workerID, attempt)
	if err != nil {
		return fmt.Errorf("CompleteTask failed: %w", err)
	}
	rowsAff, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAff == 0 {
		return ErrFencingTokenMismatch
	}
	return nil
}

// FailTask marks a task failed or reschedules it for retry, verifying the fencing token.
func FailTask(ctx context.Context, tx domain.Tx, taskID, workerID string, attempt int, retryAfter *time.Duration, errMsg string) error {
	exec := getExecutor(tx)
	if exec == nil {
		return fmt.Errorf("no database connection available")
	}

	var maxAttempts int
	err := exec.QueryRowContext(ctx, `SELECT max_attempts FROM tasks WHERE id = ?`, taskID).Scan(&maxAttempts)
	if err != nil {
		return fmt.Errorf("FailTask lookup max_attempts failed: %w", err)
	}

	if attempt >= maxAttempts || retryAfter == nil {
		// Mark dead (no more retries)
		query := `
			UPDATE tasks
			SET status = 'dead', lease_until = NULL
			WHERE id = ? AND lease_owner = ? AND attempt = ? AND status = 'leased'
		`
		res, err := exec.ExecContext(ctx, query, taskID, workerID, attempt)
		if err != nil {
			return fmt.Errorf("FailTask mark dead failed: %w", err)
		}
		rowsAff, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if rowsAff == 0 {
			return ErrFencingTokenMismatch
		}
		return nil
	}

	// Reschedule for retry
	runAfter := time.Now().UTC().Add(*retryAfter)
	query := `
		UPDATE tasks
		SET status = 'ready',
		    lease_owner = NULL,
		    lease_until = NULL,
		    run_after = ?
		WHERE id = ? AND lease_owner = ? AND attempt = ? AND status = 'leased'
	`
	res, err := exec.ExecContext(ctx, query, formatSQLiteTime(runAfter), taskID, workerID, attempt)
	if err != nil {
		return fmt.Errorf("FailTask reschedule failed: %w", err)
	}
	rowsAff, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAff == 0 {
		return ErrFencingTokenMismatch
	}
	return nil
}

// GetTask fetches a single task by ID.
func GetTask(ctx context.Context, taskID string) (*domain.Task, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("no database connection available")
	}

	query := `
		SELECT id, job_id, owner_id, step, resource_class, priority,
		       payload_json, status, lease_owner, lease_until, attempt,
		       max_attempts, run_after, created_at
		FROM tasks
		WHERE id = ?
	`
	row := dbConn.QueryRowContext(ctx, query, taskID)

	var (
		t             domain.Task
		stepStr       string
		classStr      string
		statusStr     string
		leaseOwner    sql.NullString
		leaseUntilRaw sql.NullString
		runAfterRaw   string
		createdAtRaw  string
	)

	err := row.Scan(
		&t.ID,
		&t.JobID,
		&t.OwnerID,
		&stepStr,
		&classStr,
		&t.Priority,
		&t.PayloadJSON,
		&statusStr,
		&leaseOwner,
		&leaseUntilRaw,
		&t.Attempt,
		&t.MaxAttempts,
		&runAfterRaw,
		&createdAtRaw,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetTask scan failed: %w", err)
	}

	t.Step = domain.StepType(stepStr)
	t.ResourceClass = domain.ResourceClass(classStr)
	t.Status = domain.TaskStatus(statusStr)
	if leaseOwner.Valid {
		t.LeaseOwner = &leaseOwner.String
	}
	if leaseUntilRaw.Valid && leaseUntilRaw.String != "" {
		if parsed, err := parseSQLiteTime(leaseUntilRaw.String); err == nil {
			t.LeaseUntil = &parsed
		}
	}
	t.RunAfter, _ = parseSQLiteTime(runAfterRaw)
	t.CreatedAt, _ = parseSQLiteTime(createdAtRaw)

	return &t, nil
}

// CancelTasksForJob marks any pending or leased tasks for a job as dead.
func CancelTasksForJob(ctx context.Context, tx domain.Tx, jobID string) error {
	exec := getExecutor(tx)
	if exec == nil {
		return fmt.Errorf("no database connection available")
	}

	query := `
		UPDATE tasks
		SET status = 'dead', lease_until = NULL
		WHERE job_id = ? AND status IN ('ready', 'leased')
	`
	_, err := exec.ExecContext(ctx, query, jobID)
	if err != nil {
		return fmt.Errorf("CancelTasksForJob failed: %w", err)
	}
	return nil
}
