// broker.go contains the in-memory per-user SSE pubsub used by a single process.
package runtime

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"whisperserver/src/internal/obs"
)

// Broker is a per-user in-memory pubsub for SSE with progress coalescing.
// This is intentionally process-local; if we later introduce multiple instances,
// this can be replaced with Redis/NATS/etc without touching HTTP handlers.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan []byte]struct{}

	progMu      sync.Mutex
	progTimers  map[string]*time.Timer
	progPayload map[string]map[string]any
}

// NewBroker creates an empty per-user broker.
func NewBroker() *Broker {
	return &Broker{
		subscribers: map[string]map[chan []byte]struct{}{},
		progTimers:  map[string]*time.Timer{},
		progPayload: map[string]map[string]any{},
	}
}

// Subscribe creates a buffered subscriber channel for the given user.
func (b *Broker) Subscribe(userID string) chan []byte {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribers[userID] == nil {
		b.subscribers[userID] = map[chan []byte]struct{}{}
	}
	b.subscribers[userID][ch] = struct{}{}
	return ch
}

// Unsubscribe removes and closes a previously registered subscriber channel.
func (b *Broker) Unsubscribe(userID string, ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if subs := b.subscribers[userID]; subs != nil {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(b.subscribers, userID)
		}
	}
	close(ch)
}

// Notify fans an update event out to every subscriber for the user.
// High-frequency job.progress events are coalesced per (user, job) over a 250ms window.
func (b *Broker) Notify(userID, eventType string, payload map[string]any) {
	if userID == "" {
		return
	}

	jobID, _ := payload["job_id"].(string)

	// Coalesce high-frequency job.progress events
	if eventType == "job.progress" && jobID != "" {
		key := userID + ":" + jobID
		cp := make(map[string]any, len(payload))
		for k, v := range payload {
			cp[k] = v
		}

		b.progMu.Lock()
		b.progPayload[key] = cp
		if _, exists := b.progTimers[key]; exists {
			// Timer is already running; the payload is updated to the latest snapshot
			b.progMu.Unlock()
			return
		}

		b.progTimers[key] = time.AfterFunc(250*time.Millisecond, func() {
			b.progMu.Lock()
			latest := b.progPayload[key]
			delete(b.progTimers, key)
			delete(b.progPayload, key)
			b.progMu.Unlock()

			if latest != nil {
				b.broadcast(userID, "job.progress", latest)
			}
		})
		b.progMu.Unlock()
		return
	}

	// For all other durable/lifecycle events, cancel any pending progress timer and emit immediately
	if jobID != "" {
		key := userID + ":" + jobID
		b.progMu.Lock()
		if timer, exists := b.progTimers[key]; exists {
			timer.Stop()
			delete(b.progTimers, key)
			delete(b.progPayload, key)
		}
		b.progMu.Unlock()
	}

	b.broadcast(userID, eventType, payload)
}

func (b *Broker) broadcast(userID, eventType string, payload map[string]any) {
	b.BroadcastWithSeq(userID, 0, eventType, payload)
}

// BroadcastWithSeq emits an SSE message with standard id: <seq> metadata for replay support.
func (b *Broker) BroadcastWithSeq(userID string, seq int64, eventType string, payload map[string]any) {
	obs.IncSSEReload()
	body := map[string]any{
		"type": eventType,
		"at":   time.Now().Format(time.RFC3339Nano),
	}
	if seq > 0 {
		body["seq"] = seq
	}
	for k, v := range payload {
		body[k] = v
	}
	data, err := json.Marshal(body)
	if err != nil {
		return
	}

	var message []byte
	if seq > 0 {
		message = []byte(fmt.Sprintf("id: %d\nevent: update\ndata: %s\n\n", seq, data))
	} else {
		message = []byte(fmt.Sprintf("event: update\ndata: %s\n\n", data))
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers[userID] {
		select {
		case ch <- message:
		default:
		}
	}
}
