package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"whisperserver/src/internal/domain"
)

// RecordJobStep records or updates a step execution attempt for provenance and auditability.
func RecordJobStep(ctx context.Context, tx domain.Tx, step domain.JobStep) error {
	exec := getExecutor(tx)
	if exec == nil {
		return fmt.Errorf("no database connection available")
	}

	if step.Attempt <= 0 {
		step.Attempt = 1
	}
	if step.StartedAt.IsZero() {
		step.StartedAt = time.Now().UTC()
	}

	var finishedAtStr *string
	if step.FinishedAt != nil {
		s := formatSQLiteTime(*step.FinishedAt)
		finishedAtStr = &s
	}

	query := `
		INSERT INTO job_steps (
			job_id, step, attempt, status, input_hash, artifact_key,
			engine, model, prompt_sha, params_json, quality_report,
			error_message, started_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, step, attempt) DO UPDATE SET
			status = excluded.status,
			input_hash = excluded.input_hash,
			artifact_key = excluded.artifact_key,
			engine = excluded.engine,
			model = excluded.model,
			prompt_sha = excluded.prompt_sha,
			params_json = excluded.params_json,
			quality_report = excluded.quality_report,
			error_message = excluded.error_message,
			finished_at = excluded.finished_at
	`

	_, err := exec.ExecContext(ctx, query,
		step.JobID,
		string(step.Step),
		step.Attempt,
		string(step.Status),
		step.InputHash,
		step.ArtifactKey,
		step.Engine,
		step.Model,
		step.PromptSHA,
		step.ParamsJSON,
		step.QualityReport,
		step.ErrorMessage,
		formatSQLiteTime(step.StartedAt),
		finishedAtStr,
	)
	if err != nil {
		return fmt.Errorf("RecordJobStep failed: %w", err)
	}
	return nil
}

// ListJobSteps returns the step execution history for a job, validating owner ownership.
func ListJobSteps(ctx context.Context, ownerID, jobID string) ([]domain.JobStep, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("no database connection available")
	}

	// Verify owner
	var count int
	err := dbConn.QueryRowContext(ctx, `SELECT COUNT(1) FROM jobs WHERE id = ? AND owner_id = ?`, jobID, ownerID).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("ListJobSteps owner check failed: %w", err)
	}
	if count == 0 {
		return nil, errors.New("job not found or unauthorized")
	}

	query := `
		SELECT job_id, step, attempt, status, input_hash, artifact_key,
		       engine, model, prompt_sha, params_json, quality_report,
		       error_message, started_at, finished_at
		FROM job_steps
		WHERE job_id = ?
		ORDER BY rowid ASC
	`
	rows, err := dbConn.QueryContext(ctx, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("ListJobSteps query failed: %w", err)
	}
	defer rows.Close()

	var steps []domain.JobStep
	for rows.Next() {
		var (
			s             domain.JobStep
			stepStr       string
			statusStr     string
			inputHash     sql.NullString
			artifactKey   sql.NullString
			engine        sql.NullString
			model         sql.NullString
			promptSHA     sql.NullString
			paramsJSON    sql.NullString
			qualityReport sql.NullString
			errorMsg      sql.NullString
			startedAtRaw  string
			finishedAtRaw sql.NullString
		)
		err := rows.Scan(
			&s.JobID,
			&stepStr,
			&s.Attempt,
			&statusStr,
			&inputHash,
			&artifactKey,
			&engine,
			&model,
			&promptSHA,
			&paramsJSON,
			&qualityReport,
			&errorMsg,
			&startedAtRaw,
			&finishedAtRaw,
		)
		if err != nil {
			return nil, fmt.Errorf("ListJobSteps scan failed: %w", err)
		}
		s.Step = domain.StepType(stepStr)
		s.Status = domain.StepStatus(statusStr)
		s.InputHash = inputHash.String
		s.ArtifactKey = artifactKey.String
		s.Engine = engine.String
		s.Model = model.String
		s.PromptSHA = promptSHA.String
		s.ParamsJSON = paramsJSON.String
		s.QualityReport = qualityReport.String
		s.ErrorMessage = errorMsg.String
		s.StartedAt, _ = parseSQLiteTime(startedAtRaw)
		if finishedAtRaw.Valid && finishedAtRaw.String != "" {
			if t, err := parseSQLiteTime(finishedAtRaw.String); err == nil {
				s.FinishedAt = &t
			}
		}
		steps = append(steps, s)
	}

	return steps, rows.Err()
}

