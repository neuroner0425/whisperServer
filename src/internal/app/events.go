package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
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

func apiEventsHandler(c echo.Context) error {
	u, err := currentUserOrUnauthorized(c)
	if err != nil {
		return nil
	}

	res := c.Response()
	req := c.Request()
	res.Header().Set(echo.HeaderContentType, "text/event-stream")
	res.Header().Set(echo.HeaderCacheControl, "no-cache")
	res.Header().Set(echo.HeaderConnection, "keep-alive")
	res.Header().Set("X-Accel-Buffering", "no")
	res.WriteHeader(http.StatusOK)

	sub := eventBroker.Subscribe(u.ID)
	defer eventBroker.Unsubscribe(u.ID, sub)

	if _, err := res.Write([]byte("event: ready\ndata: {}\n\n")); err != nil {
		return nil
	}
	res.Flush()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-req.Context().Done():
			return nil
		case message := <-sub:
			if _, err := res.Write(message); err != nil {
				return nil
			}
			res.Flush()
		case <-ticker.C:
			if _, err := res.Write([]byte(fmt.Sprintf(": ping %d\n\n", time.Now().Unix()))); err != nil {
				return nil
			}
			res.Flush()
		}
	}
}
