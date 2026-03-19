package httpx

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type Routes struct {
	AuthMiddleware echo.MiddlewareFunc

	LoginGet        echo.HandlerFunc
	LoginPost       echo.HandlerFunc
	SignupGet       echo.HandlerFunc
	SignupPost      echo.HandlerFunc
	LogoutPost      echo.HandlerFunc
	RootRedirect    echo.HandlerFunc
	FilesRedirect   echo.HandlerFunc
	FilesList       echo.HandlerFunc
	TagsPage        echo.HandlerFunc
	TrashPage       echo.HandlerFunc
	UploadGet       echo.HandlerFunc
	UploadPost      echo.HandlerFunc
	JobsRedirect    echo.HandlerFunc
	JobsUpdates     echo.HandlerFunc
	Status          echo.HandlerFunc
	JobDetail       echo.HandlerFunc
	Download        echo.HandlerFunc
	DownloadRefined echo.HandlerFunc
	BatchDownload   echo.HandlerFunc
	BatchDelete     echo.HandlerFunc
	BatchMove       echo.HandlerFunc
	CreateTag       echo.HandlerFunc
	DeleteTag       echo.HandlerFunc
	CreateFolder    echo.HandlerFunc
	TrashFolder     echo.HandlerFunc
	RestoreFolder   echo.HandlerFunc
	RenameFolder    echo.HandlerFunc
	MoveFolder      echo.HandlerFunc
	TrashJob        echo.HandlerFunc
	RestoreJob      echo.HandlerFunc
	RenameJob       echo.HandlerFunc
	UpdateJobTags   echo.HandlerFunc
	Healthz         echo.HandlerFunc
	RefineRetry     echo.HandlerFunc
	Metrics         http.Handler
}

func (r Routes) Register(e *echo.Echo, staticDir string) {
	if r.AuthMiddleware != nil {
		e.Use(r.AuthMiddleware)
	}

	e.Static("/static", staticDir)

	e.GET("/login", r.LoginGet)
	e.POST("/login", r.LoginPost)
	e.GET("/signup", r.SignupGet)
	e.POST("/signup", r.SignupPost)
	e.POST("/logout", r.LogoutPost)

	e.GET("/", r.RootRedirect)
	e.GET("/files", r.FilesRedirect)
	e.GET("/files/home", r.FilesList)
	e.GET("/files/root", r.FilesList)
	e.GET("/files/folders/:folder_id", r.FilesList)
	e.GET("/tags", r.TagsPage)
	e.GET("/trash", r.TrashPage)
	e.GET("/upload", r.UploadGet)
	e.POST("/upload", r.UploadPost)
	e.GET("/jobs", r.JobsRedirect)
	e.GET("/jobs/updates", r.JobsUpdates)
	e.GET("/status/:job_id", r.Status)
	e.GET("/job/:job_id", r.JobDetail)
	e.GET("/download/:job_id", r.Download)
	e.GET("/download/:job_id/refined", r.DownloadRefined)
	e.POST("/batch-download", r.BatchDownload)
	e.POST("/batch-delete", r.BatchDelete)
	e.POST("/batch-move", r.BatchMove)
	e.POST("/tags", r.CreateTag)
	e.POST("/tags/delete", r.DeleteTag)
	e.POST("/folders", r.CreateFolder)
	e.POST("/folders/:folder_id/trash", r.TrashFolder)
	e.POST("/folders/:folder_id/restore", r.RestoreFolder)
	e.POST("/folders/:folder_id/rename", r.RenameFolder)
	e.POST("/folders/:folder_id/move", r.MoveFolder)
	e.POST("/job/:job_id/trash", r.TrashJob)
	e.POST("/job/:job_id/restore", r.RestoreJob)
	e.POST("/job/:job_id/rename", r.RenameJob)
	e.POST("/job/:job_id/tags", r.UpdateJobTags)
	e.GET("/healthz", r.Healthz)
	if r.Metrics != nil {
		e.GET("/metrics", echo.WrapHandler(r.Metrics))
	}
	e.POST("/job/:job_id/refine", r.RefineRetry)
}
