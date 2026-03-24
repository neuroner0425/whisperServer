package app

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type userEventBroker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan []byte]struct{}
}

func newUserEventBroker() *userEventBroker {
	return &userEventBroker{subscribers: map[string]map[chan []byte]struct{}{}}
}

func (b *userEventBroker) Subscribe(userID string) chan []byte {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribers[userID] == nil {
		b.subscribers[userID] = map[chan []byte]struct{}{}
	}
	b.subscribers[userID][ch] = struct{}{}
	return ch
}

func (b *userEventBroker) Unsubscribe(userID string, ch chan []byte) {
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

func (b *userEventBroker) Notify(userID, eventType string, payload map[string]any) {
	if userID == "" {
		return
	}
	body := map[string]any{
		"type": eventType,
		"at":   time.Now().Format(time.RFC3339Nano),
	}
	for key, value := range payload {
		body[key] = value
	}
	data, err := json.Marshal(body)
	if err != nil {
		return
	}
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
