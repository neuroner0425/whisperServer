package app

import (
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

func apiEventsHandler(c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"detail": "인증이 필요합니다."})
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
