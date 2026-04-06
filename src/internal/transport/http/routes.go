package httptransport

import "github.com/labstack/echo/v4"

type Config struct {
	StaticDir string
}

// Handlers is a wiring-only struct.
// The goal is to keep the route table in transport while implementations can move later.
type Handlers struct {
	// Auth (HTML)
	LoginGet   echo.HandlerFunc
	LoginPost  echo.HandlerFunc
	SignupGet  echo.HandlerFunc
	SignupPost echo.HandlerFunc
	LogoutPost echo.HandlerFunc

	// SPA / redirects
	RootRedirect            echo.HandlerFunc
	RedirectFilesToHome     echo.HandlerFunc
	RedirectJobsToRoot      echo.HandlerFunc
	LegacyFilesPageRedirect echo.HandlerFunc
	LegacyTrashRedirect     echo.HandlerFunc
	LegacyTagsRedirect      echo.HandlerFunc
	SPAIndex                echo.HandlerFunc
	SPAFilesPage            echo.HandlerFunc
	SPATagsPage             echo.HandlerFunc
	SPATrashPage            echo.HandlerFunc
	SPAUploadPage           echo.HandlerFunc
	SPAJobPage              echo.HandlerFunc
	SPALoginPage            echo.HandlerFunc
	SPASignupPage           echo.HandlerFunc

	// Upload / jobs pages / status / downloads
	UploadPostHTML       echo.HandlerFunc
	JobsUpdates          echo.HandlerFunc
	Status               echo.HandlerFunc
	Download             echo.HandlerFunc
	DownloadRefined      echo.HandlerFunc
	DownloadDocumentJSON echo.HandlerFunc
	BatchDownload        echo.HandlerFunc
	BatchDelete          echo.HandlerFunc
	BatchMove            echo.HandlerFunc
	CreateTagHTML        echo.HandlerFunc
	DeleteTagHTML        echo.HandlerFunc
	CreateFolderHTML     echo.HandlerFunc
	TrashFolderHTML      echo.HandlerFunc
	RestoreFolderHTML    echo.HandlerFunc
	RenameFolderHTML     echo.HandlerFunc
	MoveFolderHTML       echo.HandlerFunc
	TrashJobHTML         echo.HandlerFunc
	RestoreJobHTML       echo.HandlerFunc
	RenameJobHTML        echo.HandlerFunc
	UpdateJobTagsHTML    echo.HandlerFunc
	RefineRetryHTML      echo.HandlerFunc

	// Infra
	Healthz echo.HandlerFunc
	Metrics echo.HandlerFunc

	// API
	APIMe              echo.HandlerFunc
	APIEvents          echo.HandlerFunc
	APIAuthSignup      echo.HandlerFunc
	APIAuthLogin       echo.HandlerFunc
	APIAuthLogout      echo.HandlerFunc
	APIFiles           echo.HandlerFunc
	APIStorage         echo.HandlerFunc
	APIJobDetail       echo.HandlerFunc
	APIJobAudio        echo.HandlerFunc
	APIJobPDF          echo.HandlerFunc
	APIRetryJob        echo.HandlerFunc
	APIRetranscribeJob echo.HandlerFunc
	APIRefineJob       echo.HandlerFunc
	APIRerefineJob     echo.HandlerFunc
	APITagsList        echo.HandlerFunc
	APITagsCreate      echo.HandlerFunc
	APITagsDelete      echo.HandlerFunc
	APIUpdateJobTags   echo.HandlerFunc
	APITrashList       echo.HandlerFunc
	APITrashClear      echo.HandlerFunc
	APITrashJobsDelete echo.HandlerFunc
	APIRestoreJob      echo.HandlerFunc
	APIRestoreFolder   echo.HandlerFunc
	APIBatchMove       echo.HandlerFunc
	APIDownloadFolder  echo.HandlerFunc
	APIUpload          echo.HandlerFunc
	APICreateFolder    echo.HandlerFunc
	APIRenameFolder    echo.HandlerFunc
	APITrashFolder     echo.HandlerFunc
	APIRenameJob       echo.HandlerFunc
	APITrashJob        echo.HandlerFunc
}

func RegisterRoutes(e *echo.Echo, cfg Config, h Handlers) {
	e.Static("/static", cfg.StaticDir)

	// Auth (legacy HTML endpoints)
	e.GET("/login", h.SPALoginPage)
	e.POST("/login", h.LoginPost)
	e.GET("/signup", h.SPASignupPage)
	e.POST("/signup", h.SignupPost)
	e.POST("/logout", h.LogoutPost)

	// Legacy pages
	e.GET("/", h.RootRedirect)
	e.GET("/files", h.RedirectFilesToHome)
	e.GET("/files/home", h.SPAFilesPage)
	e.GET("/files/root", h.SPAFilesPage)
	e.GET("/files/folders/:folder_id", h.SPAFilesPage)
	e.GET("/tags", h.SPATagsPage)
	e.GET("/trash", h.SPATrashPage)
	e.GET("/upload", h.SPAUploadPage)
	e.POST("/upload", h.UploadPostHTML)
	e.GET("/jobs", h.RedirectJobsToRoot)
	e.GET("/jobs/updates", h.JobsUpdates)
	e.GET("/status/:job_id", h.Status)
	e.GET("/job/:job_id", h.SPAJobPage)
	e.GET("/download/:job_id", h.Download)
	e.GET("/download/:job_id/refined", h.DownloadRefined)
	e.GET("/download/:job_id/document-json", h.DownloadDocumentJSON)
	e.POST("/batch-download", h.BatchDownload)
	e.POST("/batch-delete", h.BatchDelete)
	e.POST("/batch-move", h.BatchMove)
	e.POST("/tags", h.CreateTagHTML)
	e.POST("/tags/delete", h.DeleteTagHTML)
	e.POST("/folders", h.CreateFolderHTML)
	e.POST("/folders/:folder_id/trash", h.TrashFolderHTML)
	e.POST("/folders/:folder_id/restore", h.RestoreFolderHTML)
	e.POST("/folders/:folder_id/rename", h.RenameFolderHTML)
	e.POST("/folders/:folder_id/move", h.MoveFolderHTML)
	e.POST("/job/:job_id/trash", h.TrashJobHTML)
	e.POST("/job/:job_id/restore", h.RestoreJobHTML)
	e.POST("/job/:job_id/rename", h.RenameJobHTML)
	e.POST("/job/:job_id/tags", h.UpdateJobTagsHTML)
	e.POST("/job/:job_id/refine", h.RefineRetryHTML)

	// Infra
	e.GET("/healthz", h.Healthz)
	e.GET("/metrics", h.Metrics)

	// SPA router (new UI)
	e.GET("/auth/login", h.SPAIndex)
	e.GET("/auth/join", h.SPAIndex)
	e.GET("/files/trash", h.SPAIndex)
	e.GET("/files/storage", h.SPAIndex)
	e.GET("/files/search", h.SPAIndex)
	e.GET("/files/folder/:folder_id", h.SPAIndex)
	e.GET("/file/:job_id", h.SPAIndex)
	e.GET("/files/folders/:folder_id", h.LegacyFilesPageRedirect)
	e.GET("/trash", h.LegacyTrashRedirect)
	e.GET("/tags", h.LegacyTagsRedirect)

	// API
	e.GET("/api/me", h.APIMe)
	e.GET("/api/events", h.APIEvents)
	e.POST("/api/auth/signup", h.APIAuthSignup)
	e.POST("/api/auth/login", h.APIAuthLogin)
	e.POST("/api/auth/logout", h.APIAuthLogout)
	e.GET("/api/files", h.APIFiles)
	e.GET("/api/storage", h.APIStorage)
	e.GET("/api/jobs/:job_id", h.APIJobDetail)
	e.GET("/api/jobs/:job_id/audio", h.APIJobAudio)
	e.GET("/api/jobs/:job_id/pdf", h.APIJobPDF)
	e.POST("/api/jobs/:job_id/retry", h.APIRetryJob)
	e.POST("/api/jobs/:job_id/retranscribe", h.APIRetranscribeJob)
	e.POST("/api/jobs/:job_id/refine", h.APIRefineJob)
	e.POST("/api/jobs/:job_id/rerefine", h.APIRerefineJob)
	e.GET("/api/tags", h.APITagsList)
	e.POST("/api/tags", h.APITagsCreate)
	e.DELETE("/api/tags/:name", h.APITagsDelete)
	e.PUT("/api/jobs/:job_id/tags", h.APIUpdateJobTags)
	e.GET("/api/trash", h.APITrashList)
	e.POST("/api/trash/clear", h.APITrashClear)
	e.POST("/api/trash/jobs/delete", h.APITrashJobsDelete)
	e.POST("/api/jobs/:job_id/restore", h.APIRestoreJob)
	e.POST("/api/folders/:folder_id/restore", h.APIRestoreFolder)
	e.POST("/api/move", h.APIBatchMove)
	e.GET("/api/folders/:folder_id/download", h.APIDownloadFolder)
	e.POST("/api/upload", h.APIUpload)
	e.POST("/api/folders", h.APICreateFolder)
	e.PATCH("/api/folders/:folder_id", h.APIRenameFolder)
	e.DELETE("/api/folders/:folder_id", h.APITrashFolder)
	e.PATCH("/api/jobs/:job_id", h.APIRenameJob)
	e.DELETE("/api/jobs/:job_id", h.APITrashJob)

	// Frontend app entrypoint
	e.GET("/app", h.SPAIndex)
	e.GET("/app/*", h.SPAIndex)
}
