package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/repo/sqlite"
)

const (
	defaultOutboxPollInterval = 200 * time.Millisecond
	defaultOutboxBatchLimit   = 100
)

// OutboxRelay polls persisted domain events from SQLite outbox and publishes them to SSE Broker or async consumers.
type OutboxRelay struct {
	broker        *Broker
	indexJobFn   func(jobID, ownerID string) error
	logf          func(string, ...any)
	errf          func(string, error, string, ...any)
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

// NewOutboxRelay creates a transactional outbox background relay.
func NewOutboxRelay(
	broker *Broker,
	indexJobFn func(jobID, ownerID string) error,
	logf func(string, ...any),
	errf func(string, error, string, ...any),
) *OutboxRelay {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if errf == nil {
		errf = func(string, error, string, ...any) {}
	}
	return &OutboxRelay{
		broker:      broker,
		indexJobFn: indexJobFn,
		logf:        logf,
		errf:        errf,
		stopCh:      make(chan struct{}),
	}
}

// Start launches the outbox polling loop in a background goroutine.
func (r *OutboxRelay) Start(ctx context.Context) {
	r.wg.Add(1)
	go r.loop(ctx)
	r.logf("[OUTBOX_RELAY] started")
}

// Stop cleanly shuts down the relay loop.
func (r *OutboxRelay) Stop() {
	close(r.stopCh)
	r.wg.Wait()
	r.logf("[OUTBOX_RELAY] stopped")
}

func (r *OutboxRelay) loop(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(defaultOutboxPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.processBatch(ctx)
		}
	}
}

func (r *OutboxRelay) processBatch(ctx context.Context) {
	events, err := sqlite.FetchUnpublishedOutbox(ctx, defaultOutboxBatchLimit)
	if err != nil {
		r.errf("outbox.fetch", err, "failed to query unpublished events")
		return
	}
	if len(events) == 0 {
		return
	}

	publishedSeqs := make([]int64, 0, len(events))
	for _, ev := range events {
		if err := r.dispatch(ctx, ev); err != nil {
			r.errf("outbox.dispatch", err, "seq=%d event_type=%s job_id=%s", ev.Seq, ev.EventType, ev.JobID)
			// Do not break; continue with other events or mark completed ones
			continue
		}
		publishedSeqs = append(publishedSeqs, ev.Seq)
	}

	if len(publishedSeqs) > 0 {
		if err := sqlite.MarkOutboxPublished(ctx, publishedSeqs); err != nil {
			r.errf("outbox.markPublished", err, "count=%d", len(publishedSeqs))
		}
	}
}

func (r *OutboxRelay) dispatch(ctx context.Context, ev domain.DomainEvent) error {
	switch ev.EventType {
	case domain.EventTypeIndexRequested:
		if r.indexJobFn != nil && ev.JobID != "" {
			if err := r.indexJobFn(ev.JobID, ev.OwnerID); err != nil {
				return fmt.Errorf("async index failed: %w", err)
			}
			r.logf("[OUTBOX_RELAY] indexed job_id=%s owner_id=%s", ev.JobID, ev.OwnerID)
		}
	default:
		// Broadcast to SSE clients via broker
		if r.broker != nil && ev.OwnerID != "" {
			var payload map[string]any
			if ev.PayloadJSON != "" {
				_ = json.Unmarshal([]byte(ev.PayloadJSON), &payload)
			}
			if payload == nil {
				payload = make(map[string]any)
			}
			r.broker.BroadcastWithSeq(ev.OwnerID, ev.Seq, ev.EventType, payload)
		}
	}
	return nil
}

