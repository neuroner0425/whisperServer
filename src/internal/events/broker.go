package events

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Broker is a per-user in-memory pubsub for SSE.
// This is intentionally process-local; if we later introduce multiple instances,
// this can be replaced with Redis/NATS/etc without touching HTTP handlers.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan []byte]struct{}
}

func NewBroker() *Broker {
	return &Broker{subscribers: map[string]map[chan []byte]struct{}{}}
}

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

func (b *Broker) Notify(userID, eventType string, payload map[string]any) {
	if userID == "" {
		return
	}
	body := map[string]any{
		"type": eventType,
		"at":   time.Now().Format(time.RFC3339Nano),
	}
	for k, v := range payload {
		body[k] = v
	}
	data, err := json.Marshal(body)
	if err != nil {
		return
	}
	// Keep SSE event name stable ("update") so the frontend can bind once.
	message := []byte(fmt.Sprintf("event: update\ndata: %s\n\n", data))

	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers[userID] {
		select {
		case ch <- message:
		default:
		}
	}
}

