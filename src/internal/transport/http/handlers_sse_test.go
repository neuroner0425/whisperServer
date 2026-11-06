package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"whisperserver/src/internal/domain"

	"github.com/labstack/echo/v4"
)

type mockSSEBroker struct {
	subCh chan []byte
}

func (m *mockSSEBroker) Subscribe(userID string) chan []byte {
	return m.subCh
}

func (m *mockSSEBroker) Unsubscribe(userID string, ch chan []byte) {
	// noop
}

func TestEventsHandler_ReplaysMissedEvents(t *testing.T) {
	e := echo.New()
	subCh := make(chan []byte, 10)
	broker := &mockSSEBroker{subCh: subCh}

	var requestedLastSeq int64
	handlers := SSEHandlers{
		Broker: broker,
		CurrentUserOrUnauthorized: func(c echo.Context) (string, bool) {
			return "user_sse_1", true
		},
		FetchReplayEvents: func(ctx context.Context, ownerID string, lastSeq int64, limit int) ([]domain.DomainEvent, error) {
			requestedLastSeq = lastSeq
			return []domain.DomainEvent{
				{
					Seq:         43,
					OwnerID:     ownerID,
					EventType:   "job.status",
					PayloadJSON: `{"job_id":"j1","status_code":20}`,
				},
				{
					Seq:         44,
					OwnerID:     ownerID,
					EventType:   "job.status",
					PayloadJSON: `{"job_id":"j1","status_code":50}`,
				},
			}, nil
		},
	}

	reqCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(reqCtx)
	req.Header.Set("Last-Event-ID", "42")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Cancel context after short duration to terminate SSE loop
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := handlers.EventsHandler()(c)
	if err != nil {
		t.Fatalf("EventsHandler returned err: %v", err)
	}

	if requestedLastSeq != 42 {
		t.Fatalf("expected requested lastSeq 42, got %d", requestedLastSeq)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: ready") {
		t.Fatalf("expected ready event, got: %s", body)
	}
	if !strings.Contains(body, "id: 43") || !strings.Contains(body, "id: 44") {
		t.Fatalf("expected replayed events 43 and 44, got: %s", body)
	}
}

