package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"whisperserver/src/internal/domain"
)

// InsertOutboxEvent inserts a domain event into the outbox within the provided transaction.
func InsertOutboxEvent(ctx context.Context, tx domain.Tx, evt domain.DomainEvent) (int64, error) {
	exec := getExecutor(tx)
	if exec == nil {
		return 0, fmt.Errorf("no database connection available")
	}

	query := `
		INSERT INTO outbox (owner_id, job_id, event_type, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`
	createdAt := evt.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	res, err := exec.ExecContext(ctx, query,
		evt.OwnerID,
		evt.JobID,
		evt.EventType,
		evt.PayloadJSON,
		formatSQLiteTime(createdAt),
	)
	if err != nil {
		return 0, fmt.Errorf("InsertOutboxEvent failed: %w", err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// FetchUnpublishedOutbox retrieves up to limit unpublished outbox events in monotonic seq order.
func FetchUnpublishedOutbox(ctx context.Context, limit int) ([]domain.DomainEvent, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("no database connection available")
	}
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT seq, owner_id, job_id, event_type, payload_json, created_at, published_at
		FROM outbox
		WHERE published_at IS NULL
		ORDER BY seq ASC
		LIMIT ?
	`
	rows, err := dbConn.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("FetchUnpublishedOutbox failed: %w", err)
	}
	defer rows.Close()

	var events []domain.DomainEvent
	for rows.Next() {
		var (
			e            domain.DomainEvent
			jobID        sql.NullString
			createdAtRaw string
			pubAtRaw     sql.NullString
		)
		if err := rows.Scan(&e.Seq, &e.OwnerID, &jobID, &e.EventType, &e.PayloadJSON, &createdAtRaw, &pubAtRaw); err != nil {
			return nil, fmt.Errorf("FetchUnpublishedOutbox scan failed: %w", err)
		}
		if jobID.Valid {
			e.JobID = jobID.String
		}
		e.CreatedAt, _ = parseSQLiteTime(createdAtRaw)
		if pubAtRaw.Valid && pubAtRaw.String != "" {
			if t, err := parseSQLiteTime(pubAtRaw.String); err == nil {
				e.PublishedAt = &t
			}
		}
		events = append(events, e)
	}

	return events, rows.Err()
}

// MarkOutboxPublished marks a list of event sequences as published.
func MarkOutboxPublished(ctx context.Context, seqs []int64) error {
	if dbConn == nil {
		return fmt.Errorf("no database connection available")
	}
	if len(seqs) == 0 {
		return nil
	}

	placeholders := make([]string, len(seqs))
	args := make([]any, len(seqs))
	for i, s := range seqs {
		placeholders[i] = "?"
		args[i] = s
	}

	query := fmt.Sprintf(`
		UPDATE outbox
		SET published_at = CURRENT_TIMESTAMP
		WHERE seq IN (%s)
	`, strings.Join(placeholders, ","))

	_, err := dbConn.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("MarkOutboxPublished failed: %w", err)
	}
	return nil
}

// FetchOutboxSince fetches events for a specific owner strictly after lastSeq for SSE replay.
func FetchOutboxSince(ctx context.Context, ownerID string, lastSeq int64, limit int) ([]domain.DomainEvent, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("no database connection available")
	}
	if limit <= 0 {
		limit = 200
	}

	query := `
		SELECT seq, owner_id, job_id, event_type, payload_json, created_at, published_at
		FROM outbox
		WHERE owner_id = ? AND seq > ? AND event_type != 'index.requested'
		ORDER BY seq ASC
		LIMIT ?
	`
	rows, err := dbConn.QueryContext(ctx, query, ownerID, lastSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("FetchOutboxSince query failed: %w", err)
	}
	defer rows.Close()

	var events []domain.DomainEvent
	for rows.Next() {
		var (
			e            domain.DomainEvent
			jobID        sql.NullString
			createdAtRaw string
			pubAtRaw     sql.NullString
		)
		if err := rows.Scan(&e.Seq, &e.OwnerID, &jobID, &e.EventType, &e.PayloadJSON, &createdAtRaw, &pubAtRaw); err != nil {
			return nil, fmt.Errorf("FetchOutboxSince scan failed: %w", err)
		}
		if jobID.Valid {
			e.JobID = jobID.String
		}
		e.CreatedAt, _ = parseSQLiteTime(createdAtRaw)
		if pubAtRaw.Valid && pubAtRaw.String != "" {
			if t, err := parseSQLiteTime(pubAtRaw.String); err == nil {
				e.PublishedAt = &t
			}
		}
		events = append(events, e)
	}

	return events, rows.Err()
}

