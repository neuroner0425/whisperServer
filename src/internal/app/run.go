package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func Run() {
	mustEnsureDirs(staticDir, tmpFolder)
	if err := initProcessingLogger(); err != nil {
		log.Fatalf("processing logger init failed: %v", err)
	}
	defer closeProcessingLogger()
	if err := initDB(); err != nil {
		log.Fatalf("db init failed: %v", err)
	}
	defer closeDB()
	initAuthSecret()
	procLogf("[BOOT] application start")
	loadJobs()

	prometheus.MustRegister(jobsTotal, jobsInProgress, jobDurationSec, uploadBytes, queueLength)
	queueLength.Set(0)

	startWorker()
	requeuePending()

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Skipper: func(c echo.Context) bool {
			p := c.Path()
			return strings.HasPrefix(p, "/status/") || p == "/jobs/updates"
		},
	}))
	e.Renderer = mustRenderer()
	e.Use(authMiddleware)

	e.Static("/static", staticDir)

	e.GET("/login", loginGetHandler)
	e.POST("/login", loginPostHandler)
	e.GET("/signup", signupGetHandler)
	e.POST("/signup", signupPostHandler)
	e.POST("/logout", logoutPostHandler)

	e.GET("/", jobsHandler)
	e.GET("/upload", uploadGetHandler)
	e.POST("/upload", uploadPostHandler)
	e.GET("/jobs", redirectJobsToRootHandler)
	e.GET("/jobs/updates", jobsUpdatesHandler)
	e.GET("/status/:job_id", statusHandler)
	e.GET("/job/:job_id", jobHandler)
	e.GET("/download/:job_id", downloadHandler)
	e.GET("/download/:job_id/refined", downloadRefinedHandler)
	e.POST("/batch-download", batchDownloadHandler)
	e.POST("/batch-delete", batchDeleteHandler)
	e.GET("/healthz", healthzHandler)
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))
	e.POST("/job/:job_id/refine", refineRetryHandler)

	port := envString("PORT", "8000")
	go func() {
		if err := e.Start(":" + port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
		procErrf("shutdown", err, "echo shutdown failed")
	}
	procLogf("[BOOT] application stop")
	close(taskQueue)
}
