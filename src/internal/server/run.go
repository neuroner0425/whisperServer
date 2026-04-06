// run.go owns the top-level process lifecycle for the HTTP server.
package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Run creates the bootstrap, starts Echo, and performs graceful shutdown.
func Run() {
	bootstrap, err := NewBootstrap()
	if err != nil {
		log.Fatalf("%v", err)
	}

	// Start the HTTP server in the background so the main goroutine can wait on signals.
	go func() {
		if err := bootstrap.Echo.Start(":" + bootstrap.Port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Block until the process receives a termination signal.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// Give in-flight requests a short grace period before cleanup runs.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := bootstrap.Echo.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	bootstrap.Cleanup()
}
