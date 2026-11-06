package service

import (
	"context"
	"encoding/json"
	"fmt"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/repo/sqlite"
)

// StateTransitionParams defines the persistent status change for a job.
type StateTransitionParams struct {
	JobID        string
	OwnerID      string
	StatusCode   int
	StatusName   string
	StatusDetail string
	ErrorMessage string
}

// JobStateService encapsulates persistent status mutations and volatile streaming progress.
type JobStateService struct {
	txManager domain.TxManager
	broker    interface {
		Notify(userID, eventType string, payload map[string]any)
	}
}

// NewJobStateService returns a configured JobStateService.
func NewJobStateService(txManager domain.TxManager, broker interface {
	Notify(userID, eventType string, payload map[string]any)
}) *JobStateService {
	return &JobStateService{
		txManager: txManager,
		broker:    broker,
	}
}

// Transition atomically updates the job's persistent status in SQLite and records an outbox event.
func (s *JobStateService) Transition(ctx context.Context, p StateTransitionParams) error {
	if p.JobID == "" || p.OwnerID == "" {
		return fmt.Errorf("jobID and ownerID are required")
	}

	tx, err := s.txManager.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction failed: %w", err)
	}
	defer tx.Rollback()

	rawTx := sqlite.GetRawTx(tx)
	if rawTx == nil {
		return fmt.Errorf("raw transaction unavailable")
	}

	query := `
		UPDATE jobs
		SET status_code = ?,
		    version = version + 1
		WHERE id = ? AND owner_id = ?
	`
	res, err := rawTx.ExecContext(ctx, query, p.StatusCode, p.JobID, p.OwnerID)
	if err != nil {
		return fmt.Errorf("update job status failed: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("job not found or unauthorized: %s", p.JobID)
	}

	statusPayload, _ := json.Marshal(map[string]any{
		"type":          domain.EventTypeJobStatus,
		"job_id":        p.JobID,
		"status_code":   p.StatusCode,
		"status":        p.StatusName,
		"status_detail": p.StatusDetail,
		"error_message": p.ErrorMessage,
	})

	_, err = sqlite.InsertOutboxEvent(ctx, tx, domain.DomainEvent{
		OwnerID:     p.OwnerID,
		JobID:       p.JobID,
		EventType:   domain.EventTypeJobStatus,
		PayloadJSON: string(statusPayload),
	})
	if err != nil {
		return fmt.Errorf("insert outbox event failed: %w", err)
	}

	return tx.Commit()
}

// ReportProgress broadcasts transient, non-persisted progress snapshots via SSE broker without touching SQLite.
func (s *JobStateService) ReportProgress(ownerID, jobID string, snapshot domain.ProgressSnapshot) {
	if s.broker == nil || ownerID == "" || jobID == "" {
		return
	}

	payload := map[string]any{
		"job_id":               jobID,
		"status_code":          snapshot.StatusCode,
		"phase":                snapshot.Phase,
		"progress_percent":     snapshot.ProgressPercent,
		"progress_label":       snapshot.ProgressLabel,
		"preview_text":         snapshot.PreviewText,
		"page_count":           snapshot.PageCount,
		"processed_page_count": snapshot.ProcessedPageCount,
		"current_chunk":        snapshot.CurrentChunk,
		"total_chunks":         snapshot.TotalChunks,
	}

	s.broker.Notify(ownerID, domain.EventTypeJobProgress, payload)
}

