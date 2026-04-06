package httptransport

import (
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

// SSEBroker is the runtime event source used by the `/api/events` stream.
type SSEBroker interface {
	Subscribe(userID string) chan []byte
	Unsubscribe(userID string, ch chan []byte)
}

// SSEHandlers exposes the job/file event stream consumed by the frontend.
type SSEHandlers struct {
	Broker SSEBroker

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
