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
	if err := validatePDFTools(); err != nil {
		log.Fatalf("pdf tool validation failed: %v", err)
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
		SplitTaskQueues:          splitTaskQueues,
		TmpFolder:                tmpFolder,
		ModelDir:                 modelDir,
		WhisperCLI:               whisperCLI,
		JobTimeoutSec:            jobTimeoutSec,
		PDFBatchTimeoutSec:       pdfBatchTimeoutSec,
		PDFMaxPages:              pdfMaxPages,
		PDFMaxPagesPerRequest:    pdfMaxPagesPerRequest,
		PDFMaxRenderedImageBytes: pdfMaxRenderedImageBytes,
		ProgressRe:               progressRe,
		StatusPending:            statusPending,
		StatusRunning:            statusRunning,
		StatusRefiningPending:    statusRefiningPending,
		StatusRefining:           statusRefining,
		StatusCompleted:          statusCompleted,
		StatusFailed:             statusFailed,
	}, worker.Deps{
		GetJob:                getJob,
		SetJobFields:          setJobFields,
		AppendJobPreviewLine:  appendJobPreviewLine,
		ReplaceJobPreviewText: replaceJobPreviewText,
		ConvertToWav:          intutil.ConvertToWav,
		HasGeminiConfigured:   hasGeminiConfigured,
		RefineTranscript:      refineTranscript,
		CountPDFPages: func(path string) (int, error) {
			return intutil.CountPDFPages(pdfToolPDFInfo, path)
		},
		RenderPDFToJPEGs: func(pdfPath, outDir string) ([]string, error) {
			return intutil.RenderPDFToJPEGs(pdfToolPDFToPPM, pdfPath, outDir, pdfRenderDPI)
		},
		ExtractDocumentChunk:    extractDocumentChunk,
		BuildConsistencyContext: buildConsistencyContext,
		MergeDocumentJSON:       mergeDocumentJSON,
		RenderDocumentMarkdown:  renderDocumentMarkdown,
		ListJobBlobKinds:        store.ListJobBlobKinds,
		UniqueStrings:           intutil.UniqueStringsKeepOrder,
		GetTagDescriptions:      store.GetTagDescriptionsByNames,
		Logf:                    procLogf,
		Errf:                    procErrf,
		IncInProgress:           jobsInProgress.Inc,
		DecInProgress:           jobsInProgress.Dec,
		SetQueueLength:          queueLength.Set,
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
	jobsD := jobsDeps()
	uploadD := uploadDeps()
	tagD := tagDeps()
	folderD := folderDeps()
	mutationD := mutationDeps()
	trashD := trashDeps()

	e.Use(authHandlers.Middleware)
	e.Static("/static", staticDir)

	e.GET("/login", spaLoginPageHandler)
	e.POST("/login", authHandlers.LoginPostHandler)
	e.GET("/signup", spaSignupPageHandler)
	e.POST("/signup", authHandlers.SignupPostHandler)
	e.POST("/logout", authHandlers.LogoutPostHandler)

	e.GET("/", rootRedirectHandler)
	e.GET("/files", redirectFilesToHomeHandler)
	e.GET("/files/home", spaFilesPageHandler)
	e.GET("/files/root", spaFilesPageHandler)
	e.GET("/files/folders/:folder_id", spaFilesPageHandler)
	e.GET("/tags", spaTagsPageHandler)
	e.GET("/trash", spaTrashPageHandler)
	e.GET("/upload", spaUploadPageHandler)
	e.POST("/upload", func(c echo.Context) error { return httpx.UploadPostHandler(c, uploadD) })
	e.GET("/jobs", redirectJobsToRootHandler)
	e.GET("/jobs/updates", func(c echo.Context) error { return httpx.JobsUpdatesHandler(c, jobsD) })
	e.GET("/status/:job_id", func(c echo.Context) error { return httpx.StatusHandler(c, jobsD) })
	e.GET("/job/:job_id", spaJobPageHandler)
	e.GET("/download/:job_id", func(c echo.Context) error { return httpx.DownloadHandler(c, jobsD) })
	e.GET("/download/:job_id/refined", func(c echo.Context) error { return httpx.DownloadRefinedHandler(c, jobsD) })
	e.GET("/download/:job_id/document-json", func(c echo.Context) error { return httpx.DownloadDocumentJSONHandler(c, jobsD) })
	e.POST("/batch-download", func(c echo.Context) error { return httpx.BatchDownloadHandler(c, jobsD) })
	e.POST("/batch-delete", func(c echo.Context) error { return httpx.BatchDeleteHandler(c, mutationD) })
	e.POST("/batch-move", func(c echo.Context) error { return httpx.MoveJobsHandler(c, folderD) })
	e.POST("/tags", func(c echo.Context) error { return httpx.CreateTagHandler(c, tagD) })
	e.POST("/tags/delete", func(c echo.Context) error { return httpx.DeleteTagHandler(c, tagD) })
	e.POST("/folders", func(c echo.Context) error { return httpx.CreateFolderHandler(c, folderD) })
	e.POST("/folders/:folder_id/trash", func(c echo.Context) error { return httpx.TrashFolderHandler(c, trashD) })
	e.POST("/folders/:folder_id/restore", func(c echo.Context) error { return httpx.RestoreFolderHandler(c, trashD) })
	e.POST("/folders/:folder_id/rename", func(c echo.Context) error { return httpx.RenameFolderHandler(c, folderD) })
	e.POST("/folders/:folder_id/move", func(c echo.Context) error { return httpx.MoveFolderHandler(c, folderD) })
	e.POST("/job/:job_id/trash", func(c echo.Context) error { return httpx.TrashJobHandler(c, trashD) })
	e.POST("/job/:job_id/restore", func(c echo.Context) error { return httpx.RestoreJobHandler(c, trashD) })
	e.POST("/job/:job_id/rename", func(c echo.Context) error { return httpx.RenameJobHandler(c, trashD) })
	e.POST("/job/:job_id/tags", func(c echo.Context) error { return httpx.UpdateJobTagsHandler(c, tagD) })
	e.GET("/healthz", httpx.HealthzHandler)
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))
	e.POST("/job/:job_id/refine", func(c echo.Context) error { return httpx.RefineRetryHandler(c, jobsD) })

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
	e.GET("/api/me", apiMeJSONHandler)
	e.GET("/api/events", apiEventsHandler)
	e.POST("/api/auth/signup", authHandlers.SignupJSONHandler)
	e.POST("/api/auth/login", authHandlers.LoginJSONHandler)
	e.POST("/api/auth/logout", authHandlers.LogoutJSONHandler)
	e.GET("/api/files", apiFilesJSONHandler)
	e.GET("/api/storage", apiStorageJSONHandler)
	e.GET("/api/jobs/:job_id", apiJobDetailJSONHandler)
	e.GET("/api/jobs/:job_id/audio", apiJobAudioHandler)
	e.GET("/api/jobs/:job_id/pdf", apiJobPDFHandler)
	e.POST("/api/jobs/:job_id/retry", apiRetryJobJSONHandler)
	e.POST("/api/jobs/:job_id/retranscribe", apiRetranscribeJobJSONHandler)
	e.POST("/api/jobs/:job_id/refine", apiRefineJobJSONHandler)
	e.POST("/api/jobs/:job_id/rerefine", apiRerefineJobJSONHandler)
	e.GET("/api/tags", func(c echo.Context) error { return httpx.TagsJSONHandler(c, tagD) })
	e.POST("/api/tags", func(c echo.Context) error { return httpx.CreateTagJSONHandler(c, tagD) })
	e.DELETE("/api/tags/:name", func(c echo.Context) error { return httpx.DeleteTagJSONHandler(c, tagD) })
	e.PUT("/api/jobs/:job_id/tags", func(c echo.Context) error { return httpx.UpdateJobTagsJSONHandler(c, tagD) })
	e.GET("/api/trash", apiTrashListJSONHandler)
	e.POST("/api/trash/clear", clearTrashJSONHandler)
	e.POST("/api/trash/jobs/delete", deleteTrashJobsJSONHandler)
	e.POST("/api/jobs/:job_id/restore", apiRestoreJobJSONHandler)
	e.POST("/api/folders/:folder_id/restore", apiRestoreFolderJSONHandler)
	e.POST("/api/move", apiBatchMoveJSONHandler)
	e.GET("/api/folders/:folder_id/download", downloadFolderJSONHandler)
	e.POST("/api/upload", func(c echo.Context) error { return httpx.UploadJSONHandler(c, uploadD) })
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
