// bootstrap.go builds the full server runtime and returns a ready-to-run Bootstrap.
package server

import (
	"fmt"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"

	store "whisperserver/src/internal/repo/sqlite"
	intruntime "whisperserver/src/internal/runtime"
	intutil "whisperserver/src/internal/util"
)

var metricsOnce sync.Once

type Bootstrap struct {
	Echo    *echo.Echo
	Port    string
	Cleanup func()
}

// NewBootstrap initializes config, persistence, runtime state, workers, and HTTP wiring.
func NewBootstrap() (*Bootstrap, error) {
	// Prepare required local directories and validate config/tooling before opening services.
	intutil.MustEnsureDirs(staticDir, tmpFolder)
	if err := initRuntimeConfig(); err != nil {
		return nil, fmt.Errorf("config init failed: %w", err)
	}
	if err := validatePDFTools(); err != nil {
		return nil, fmt.Errorf("pdf tool validation failed: %w", err)
	}
	if err := initProcessingLogger(); err != nil {
		return nil, fmt.Errorf("processing logger init failed: %w", err)
	}

	procLogf("[BOOT] config source=%s", configPath)
	store.ConfigureLogging(procLogf, procErrf)
	if err := store.Init(projectRoot); err != nil {
		closeProcessingLogger()
		return nil, fmt.Errorf("db init failed: %w", err)
	}

	initAuthRuntime()
	procLogf("[BOOT] application start")

	// Build the process-local runtime around the persisted job snapshot.
	appRuntime = intruntime.New(intruntime.Config{
		TmpFolder:             tmpFolder,
		Now:                   time.Now,
		LoadJobs:              store.LoadJobs,
		SaveJobs:              store.SaveJobs,
		DeleteJobBlobs:        store.DeleteJobBlobs,
		SaveJobBlob:           store.SaveJobBlob,
		ListAllFoldersByOwner: store.ListAllFoldersByOwner,
		GetFolderByID:         store.GetFolderByID,
		SetFolderTrashed:      store.SetFolderTrashed,
		Errf:                  procErrf,
	})
	appRuntime.LoadJobs()
	appRuntime.CleanupInactiveTempWavs()

	// Register metrics only once even if tests create multiple bootstraps.
	metricsOnce.Do(func() {
		prometheus.MustRegister(jobsTotal, jobsInProgress, jobDurationSec, uploadBytes, queueLength)
	})
	queueLength.Set(0)

	// Assemble services and start the background worker before opening HTTP routes.
	svc := newAppServices()
	appWorker := newAppWorker(svc.blobSvc, svc.geminiRt, svc.whisperRt)
	appRuntime.SetWorker(appWorker)
	appWorker.Start()
	appRuntime.RequeuePending()

	e := newHTTPServer()
	registerHTTPRoutes(e, svc)
	port, err := appPort()
	if err != nil {
		if appWorker != nil {
			appWorker.Close()
			appRuntime.SetWorker(nil)
		}
		store.Close()
		closeProcessingLogger()
		return nil, fmt.Errorf("port config failed: %w", err)
	}
	// Cleanup mirrors the startup order in reverse.
	return &Bootstrap{
		Echo: e,
		Port: port,
		Cleanup: func() {
			procLogf("[BOOT] application stop")
			if appWorker != nil {
				appWorker.Close()
				appRuntime.SetWorker(nil)
			}
			store.Close()
			closeProcessingLogger()
		},
	}, nil
}
