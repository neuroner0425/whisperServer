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
	httpx "whisperserver/src/internal/http"
	"whisperserver/src/internal/store"
	intutil "whisperserver/src/internal/util"
	"whisperserver/src/internal/view"
	"whisperserver/src/internal/worker"
)

func Run() {
	intutil.MustEnsureDirs(staticDir, tmpFolder)
	if err := initRuntimeConfig(); err != nil {
		log.Fatalf("config init failed: %v", err)
	}
	if err := initProcessingLogger(); err != nil {
		log.Fatalf("processing logger init failed: %v", err)
	}
	defer closeProcessingLogger()
	procLogf("[BOOT] config source=%s", configPath)
	store.ConfigureLogging(procLogf, procErrf)
	if err := store.Init(projectRoot); err != nil {
		log.Fatalf("db init failed: %v", err)
	}
	defer store.Close()
	initAuthSecret()
	initAuthHandlers()
	procLogf("[BOOT] application start")
	loadJobs()

	prometheus.MustRegister(jobsTotal, jobsInProgress, jobDurationSec, uploadBytes, queueLength)
	queueLength.Set(0)

	appWorker = worker.New(worker.Config{
		SplitTaskQueues:       splitTaskQueues,
		ModelDir:              modelDir,
		WhisperCLI:            whisperCLI,
		JobTimeoutSec:         jobTimeoutSec,
		ProgressRe:            progressRe,
		StatusPending:         statusPending,
		StatusRunning:         statusRunning,
		StatusRefiningPending: statusRefiningPending,
		StatusRefining:        statusRefining,
		StatusCompleted:       statusCompleted,
		StatusFailed:          statusFailed,
	}, worker.Deps{
		GetJob:               getJob,
		SetJobFields:         setJobFields,
		AppendJobPreviewLine: appendJobPreviewLine,
		HasGeminiConfigured:  hasGeminiConfigured,
		RefineTranscript:     refineTranscript,
		UniqueStrings:        intutil.UniqueStringsKeepOrder,
		GetTagDescriptions:   store.GetTagDescriptionsByNames,
		Logf:                 procLogf,
		Errf:                 procErrf,
		IncInProgress:        jobsInProgress.Inc,
		DecInProgress:        jobsInProgress.Dec,
		SetQueueLength:       queueLength.Set,
		IncJobsTotal: func(status string) {
			jobsTotal.WithLabelValues(status).Inc()
		},
		ObserveJobDuration: jobDurationSec.Observe,
	})
	appWorker.Start()
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
	e.Renderer = view.MustRenderer(templateDir)
	httpx.Routes{
		AuthMiddleware:  authMiddleware,
		LoginGet:        loginGetHandler,
		LoginPost:       loginPostHandler,
		SignupGet:       signupGetHandler,
		SignupPost:      signupPostHandler,
		LogoutPost:      logoutPostHandler,
		RootRedirect:    rootRedirectHandler,
		FilesRedirect:   redirectFilesToHomeHandler,
		FilesList:       jobsHandler,
		TagsPage:        tagsPageHandler,
		TrashPage:       trashHandler,
		UploadGet:       uploadGetHandler,
		UploadPost:      uploadPostHandler,
		JobsRedirect:    redirectJobsToRootHandler,
		JobsUpdates:     jobsUpdatesHandler,
		Status:          statusHandler,
		JobDetail:       jobHandler,
		Download:        downloadHandler,
		DownloadRefined: downloadRefinedHandler,
		BatchDownload:   batchDownloadHandler,
		BatchDelete:     batchDeleteHandler,
		BatchMove:       moveJobsHandler,
		CreateTag:       createTagHandler,
		DeleteTag:       deleteTagHandler,
		CreateFolder:    createFolderHandler,
		TrashFolder:     trashFolderHandler,
		RestoreFolder:   restoreFolderHandler,
		RenameFolder:    renameFolderHandler,
		MoveFolder:      moveFolderHandler,
		TrashJob:        trashJobHandler,
		RestoreJob:      restoreJobHandler,
		RenameJob:       renameJobHandler,
		UpdateJobTags:   updateJobTagsHandler,
		Healthz:         healthzHandler,
		RefineRetry:     refineRetryHandler,
		Metrics:         promhttp.Handler(),
	}.Register(e, staticDir)

	port, err := appPort()
	if err != nil {
		log.Fatalf("port config failed: %v", err)
	}
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
	if appWorker != nil {
		appWorker.Close()
	}
}
