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
	cleanupInactiveTempWavs()

	prometheus.MustRegister(jobsTotal, jobsInProgress, jobDurationSec, uploadBytes, queueLength)
	queueLength.Set(0)

	appWorker = worker.New(worker.Config{
		SplitTaskQueues:       splitTaskQueues,
		TmpFolder:             tmpFolder,
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
		LoginGet:        spaLoginPageHandler,
		LoginPost:       loginPostHandler,
		SignupGet:       spaSignupPageHandler,
		SignupPost:      signupPostHandler,
		LogoutPost:      logoutPostHandler,
		RootRedirect:    rootRedirectHandler,
		FilesRedirect:   redirectFilesToHomeHandler,
		FilesList:       spaFilesPageHandler,
		TagsPage:        spaTagsPageHandler,
		TrashPage:       spaTrashPageHandler,
		UploadGet:       spaUploadPageHandler,
		UploadPost:      uploadPostHandler,
		JobsRedirect:    redirectJobsToRootHandler,
		JobsUpdates:     jobsUpdatesHandler,
		Status:          statusHandler,
		JobDetail:       spaJobPageHandler,
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
	e.GET("/auth/login", spaIndexHandler)
	e.GET("/auth/join", spaIndexHandler)
	e.GET("/files/trash", spaIndexHandler)
	e.GET("/files/storage", spaIndexHandler)
	e.GET("/files/search", spaIndexHandler)
	e.GET("/files/folder/:folder_id", spaIndexHandler)
	e.GET("/file/:job_id", spaIndexHandler)
	e.GET("/files/folders/:folder_id", legacyFilesPageRedirectHandler)
	e.GET("/trash", func(c echo.Context) error { return c.Redirect(http.StatusSeeOther, "/files/trash") })
	e.GET("/tags", func(c echo.Context) error { return c.Redirect(http.StatusSeeOther, "/files/home") })
	e.GET("/api/me", apiMeHandler)
	e.GET("/api/events", apiEventsStreamHandler)
	e.POST("/api/auth/signup", signupJSONHandler)
	e.POST("/api/auth/login", loginJSONHandler)
	e.POST("/api/auth/logout", logoutJSONHandler)
	e.GET("/api/files", apiFilesHandler)
	e.GET("/api/storage", apiStorageJSONHandler)
	e.GET("/api/jobs/:job_id", apiJobDetailHandler)
	e.GET("/api/tags", apiTagsHandler)
	e.POST("/api/tags", apiCreateTagHandler)
	e.DELETE("/api/tags/:name", apiDeleteTagHandler)
	e.PUT("/api/jobs/:job_id/tags", apiUpdateJobTagsHandler)
	e.GET("/api/trash", apiTrashListHandler)
	e.POST("/api/trash/clear", apiClearTrashHandler)
	e.POST("/api/trash/jobs/delete", apiDeleteTrashJobsHandler)
	e.POST("/api/jobs/:job_id/restore", apiRestoreJobHandler)
	e.POST("/api/folders/:folder_id/restore", apiRestoreFolderHandler)
	e.POST("/api/move", apiBatchMoveHandler)
	e.GET("/api/folders/:folder_id/download", apiDownloadFolderHandler)
	e.POST("/api/upload", apiUploadJSONHandler)
	e.POST("/api/folders", apiCreateFolderJSONHandler)
	e.PATCH("/api/folders/:folder_id", apiRenameFolderJSONHandler)
	e.DELETE("/api/folders/:folder_id", apiTrashFolderJSONHandler)
	e.PATCH("/api/jobs/:job_id", apiRenameJobJSONHandler)
	e.DELETE("/api/jobs/:job_id", apiTrashJobJSONHandler)
	e.GET("/app", spaIndexHandler)
	e.GET("/app/*", spaIndexHandler)

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
