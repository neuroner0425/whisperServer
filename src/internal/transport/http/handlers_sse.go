package httptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"whisperserver/src/internal/domain"

	"github.com/labstack/echo/v4"
)

// SSEBroker is the runtime event source used by the `/api/events` stream.
type SSEBroker interface {
	Subscribe(userID string) chan []byte
	Unsubscribe(userID string, ch chan []byte)
}

// SSEHandlers exposes the job/file event stream consumed by the frontend.
type SSEHandlers struct {
	Broker            SSEBroker
	FetchReplayEvents func(ctx context.Context, ownerID string, lastSeq int64, limit int) ([]domain.DomainEvent, error)

	// CurrentUserOrUnauthorized should have already written 401 JSON on failure.
	CurrentUserOrUnauthorized func(echo.Context) (userID string, ok bool)
}

// EventsHandler opens an SSE stream and forwards broker messages to the client.
func (h SSEHandlers) EventsHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.Broker == nil || h.CurrentUserOrUnauthorized == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		userID, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || userID == "" {
			return nil
		}

		res := c.Response()
		req := c.Request()
		res.Header().Set(echo.HeaderContentType, "text/event-stream")
		res.Header().Set(echo.HeaderCacheControl, "no-cache")
		res.Header().Set(echo.HeaderConnection, "keep-alive")
		res.Header().Set("X-Accel-Buffering", "no")
		res.WriteHeader(http.StatusOK)

		// Subscribe after headers are committed so the client can start reading immediately.
		sub := h.Broker.Subscribe(userID)
		defer h.Broker.Unsubscribe(userID, sub)

		if _, err := res.Write([]byte("event: ready\ndata: {}\n\n")); err != nil {
			return nil
		}
		res.Flush()

		// SSE Replay: Check for Last-Event-ID header and replay missed events
		lastEventIDStr := req.Header.Get("Last-Event-ID")
		if lastEventIDStr != "" && h.FetchReplayEvents != nil {
			if lastSeq, err := strconv.ParseInt(lastEventIDStr, 10, 64); err == nil && lastSeq > 0 {
				replayed, err := h.FetchReplayEvents(req.Context(), userID, lastSeq, 200)
				if err == nil {
					for _, ev := range replayed {
						var payload map[string]any
						if err := json.Unmarshal([]byte(ev.PayloadJSON), &payload); err != nil {
							payload = map[string]any{"raw": ev.PayloadJSON}
						}
						body := map[string]any{
							"type": ev.EventType,
							"seq":  ev.Seq,
							"at":   ev.CreatedAt.Format(time.RFC3339Nano),
						}
						for k, v := range payload {
							body[k] = v
						}
						data, err := json.Marshal(body)
						if err != nil {
							continue
						}
						msg := fmt.Sprintf("id: %d\nevent: update\ndata: %s\n\n", ev.Seq, data)
						if _, err := res.Write([]byte(msg)); err != nil {
							return nil
						}
					}
					res.Flush()
				}
			}
		}

		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()

		// Keep the connection alive with periodic comments between real events.
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
}
