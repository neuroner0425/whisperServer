package server

import (
	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	model "whisperserver/src/internal/domain"
	store "whisperserver/src/internal/repo/sqlite"
	httptransport "whisperserver/src/internal/transport/http"
	intutil "whisperserver/src/internal/util"
)

// buildRouteHandlers assembles the concrete handler set consumed by the route table.
func buildRouteHandlers(svc appServices, spaIndex echo.HandlerFunc) httptransport.Handlers {
	filesQ := newFilesQuery()
	uploadH := newUploadHandlers(svc)
	legacyJobsH := newLegacyJobsHandlers(svc, filesQ)
	legacyRefineH := newLegacyRefineHandlers(svc)
	legacyMutationH := newLegacyMutationHandlers(svc)

	return httptransport.Handlers{
		LoginPost:               authRuntime.LoginPostHandler,
		SignupPost:              authRuntime.SignupPostHandler,
		LogoutPost:              authRuntime.LogoutPostHandler,
		RootRedirect:            newSPAHandlers().RootRedirectHandler(),
		RedirectFilesToHome:     httptransport.RedirectFilesToHomeHandler,
		RedirectJobsToRoot:      httptransport.RedirectJobsToRootHandler,
		LegacyFilesPageRedirect: httptransport.LegacyFilesPageRedirectHandler,
		LegacyTrashRedirect:     httptransport.LegacyTrashRedirectHandler,
		LegacyTagsRedirect:      httptransport.LegacyTagsRedirectHandler,
		SPAIndex:                spaIndex,
		SPAFilesPage:            httptransport.SPAFilesPageHandler(spaIndex),
		SPATagsPage:             httptransport.SPATagsPageHandler,
		SPATrashPage:            httptransport.SPATrashPageHandler(spaIndex),
		SPAUploadPage:           httptransport.SPAUploadPageHandler,
		SPAJobPage:              httptransport.SPAJobPageHandler,
		SPALoginPage:            httptransport.SPALoginPageHandler,
		SPASignupPage:           httptransport.SPASignupPageHandler,
		UploadPostHTML:          uploadH.PostHTML(),
		JobsUpdates:             legacyJobsH.Updates(),
		Status:                  legacyJobsH.Status(),
		Download:                legacyJobsH.Download(),
		DownloadRefined:         legacyJobsH.DownloadRefined(),
		DownloadDocumentJSON:    legacyJobsH.DownloadDocumentJSON(),
		BatchDownload:           legacyJobsH.BatchDownload(),
		BatchDelete:             legacyMutationH.BatchDelete(),
		BatchMove:               legacyMutationH.BatchMove(),
		CreateTagHTML:           legacyMutationH.CreateTagHTML(),
		DeleteTagHTML:           legacyMutationH.DeleteTagHTML(),
		CreateFolderHTML:        legacyMutationH.CreateFolderHTML(),
		TrashFolderHTML:         legacyMutationH.TrashFolderHTML(),
		RestoreFolderHTML:       legacyMutationH.RestoreFolderHTML(),
		RenameFolderHTML:        legacyMutationH.RenameFolderHTML(),
		MoveFolderHTML:          legacyMutationH.MoveFolderHTML(),
		TrashJobHTML:            legacyMutationH.TrashJobHTML(),
		RestoreJobHTML:          legacyMutationH.RestoreJobHTML(),
		RenameJobHTML:           legacyMutationH.RenameJobHTML(),
		UpdateJobTagsHTML:       legacyMutationH.UpdateJobTagsHTML(),
		RefineRetryHTML:         legacyRefineH.RetryHTML(),
		Healthz:                 httptransport.HealthzHandler,
		Metrics:                 echo.WrapHandler(promhttp.Handler()),
		APIMe:                   httptransport.MeHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized}.Handler(),
		APIEvents:               newEventsHandler(),
		APIAuthSignup:           authRuntime.SignupJSONHandler,
		APIAuthLogin:            authRuntime.LoginJSONHandler,
		APIAuthLogout:           authRuntime.LogoutJSONHandler,
		APIFiles: httptransport.FilesHandlers{
			CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized,
			CurrentUserName:           currentUserName,
			FolderSvc:                 svc.folderSvc,
			TagSvc:                    svc.tagSvc,
			BuildRecentJobRows:        filesQ.BuildRecentJobRowsForUser,
			BuildJobRows:              filesQ.BuildJobRowsForUser,
			BuildFolderRows:           filesQ.BuildFolderRowsForUser,
			RecentFolderRows:          filesQ.RecentFolderRowsForUser,
			SortJobRows: func(rows []httptransport.JobRow, sortBy, sortOrder string) {
				httptransport.SortJobRows(rows, sortBy, sortOrder, svc.runtime.UploadedTS)
			},
			SortFolderRows:    httptransport.SortFolderRows,
			PaginateRows:      httptransport.PaginateJobRows,
			SnapshotVersion:   httptransport.JobsSnapshotVersion,
			PDFMaxPages:       pdfMaxPages,
			PDFMaxPagesPerReq: pdfMaxPagesPerRequest,
		}.Handler(),
		APIStorage: httptransport.StorageHandlers{
			CapacityBytes:             5 * 1024 * 1024 * 1024,
			CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized,
			JobsSnapshot:              jobsSnapshot,
			StorageSvc:                svc.storageSvc,
			FolderSvc:                 svc.folderSvc,
		}.Handler(),
		APIJobDetail: httptransport.JobDetailHandlers{
			CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized,
			CurrentUserName:           currentUserName,
			GetJob:                    svc.runtime.GetJob,
			ToJobView:                 func(j *model.Job) any { return toAPIJobView(j) },
			HasGeminiConfigured:       svc.geminiRt.HasConfigured,
			TagSvc:                    svc.tagSvc,
			BlobSvc:                   svc.blobSvc,
			StatusCompleted:           statusCompleted,
			StatusRefiningPending:     statusRefiningPending,
			StatusRefining:            statusRefining,
		}.DetailJSON(),
		APIJobAudio:        httptransport.JobDetailHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, GetJob: svc.runtime.GetJob, BlobSvc: svc.blobSvc}.Audio(),
		APIJobPDF:          httptransport.JobDetailHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, GetJob: svc.runtime.GetJob, BlobSvc: svc.blobSvc}.PDF(),
		APIRetryJob:        httptransport.JobControlHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, GetJob: svc.runtime.GetJob, BlobSvc: svc.blobSvc, HasJobBlob: store.HasJobBlob, PrepareForPDFRetry: svc.lifecycle.PrepareForPDFRetry, ResetForTranscribe: svc.lifecycle.ResetForTranscribe, ResetForRefine: svc.lifecycle.ResetForRefine, EnqueuePDFExtract: svc.runtime.EnqueuePDFExtract, EnqueueTranscribe: svc.runtime.EnqueueTranscribe, EnqueueRefine: svc.runtime.EnqueueRefine, HasGeminiConfigured: svc.geminiRt.HasConfigured, StatusFailed: statusFailed, BlobAudioAAC: store.BlobKindAudioAAC, BlobPDFOriginal: store.BlobKindPDFOriginal}.Retry(),
		APIRetranscribeJob: httptransport.JobControlHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, GetJob: svc.runtime.GetJob, BlobSvc: svc.blobSvc, HasJobBlob: store.HasJobBlob, ResetForPDF: svc.lifecycle.ResetForPDF, ResetForTranscribe: svc.lifecycle.ResetForTranscribe, EnqueuePDFExtract: svc.runtime.EnqueuePDFExtract, EnqueueTranscribe: svc.runtime.EnqueueTranscribe, StatusCompleted: statusCompleted, BlobAudioAAC: store.BlobKindAudioAAC, BlobPDFOriginal: store.BlobKindPDFOriginal}.Retranscribe(),
		APIRefineJob:       httptransport.JobControlHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, GetJob: svc.runtime.GetJob, SetJobFields: svc.runtime.SetJobFields, BlobSvc: svc.blobSvc, EnqueueRefine: svc.runtime.EnqueueRefine, HasGeminiConfigured: svc.geminiRt.HasConfigured, StatusCompleted: statusCompleted, StatusRefiningPending: statusRefiningPending}.Refine(),
		APIRerefineJob:     httptransport.JobControlHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, GetJob: svc.runtime.GetJob, SetJobFields: svc.runtime.SetJobFields, BlobSvc: svc.blobSvc, EnqueueRefine: svc.runtime.EnqueueRefine, HasGeminiConfigured: svc.geminiRt.HasConfigured, StatusCompleted: statusCompleted, StatusRefiningPending: statusRefiningPending}.Rerefine(),
		APITagsList:        httptransport.TagsHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, TagSvc: svc.tagSvc, Errf: procErrf}.List(),
		APITagsCreate:      httptransport.TagsHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, TagSvc: svc.tagSvc, IsValidTagName: intutil.IsValidTagName, Logf: procLogf, Errf: procErrf}.Create(),
		APITagsDelete:      httptransport.TagsHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, TagSvc: svc.tagSvc, RemoveTagFromOwnerJobs: svc.runtime.RemoveTagFromOwnerJobs, Logf: procLogf, Errf: procErrf}.Delete(),
		APIUpdateJobTags:   httptransport.TagsHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, TagSvc: svc.tagSvc, GetJob: svc.runtime.GetJob, SetJobFields: svc.runtime.SetJobFields, Logf: procLogf, Errf: procErrf}.UpdateJobTags(),
		APITrashList:       httptransport.TrashHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, BlobSvc: svc.blobSvc, BuildJobRowsForUser: filesQ.BuildJobRowsForUser}.List(),
		APITrashClear:      httptransport.TrashHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, BlobSvc: svc.blobSvc, JobsSnapshot: jobsSnapshot, DeleteJobsFn: svc.runtime.DeleteJobs, NotifyFilesChanged: svc.lifecycle.NotifyFilesChanged}.Clear(),
		APITrashJobsDelete: httptransport.TrashHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, BlobSvc: svc.blobSvc, JobsSnapshot: jobsSnapshot, DeleteJobsFn: svc.runtime.DeleteJobs, NotifyFilesChanged: svc.lifecycle.NotifyFilesChanged}.DeleteTrashJobs(),
		APIRestoreJob:      httptransport.TrashHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, BlobSvc: svc.blobSvc, GetJob: svc.runtime.GetJob, SetJobFields: svc.runtime.SetJobFields, EnqueueTranscribe: svc.runtime.EnqueueTranscribe, EnqueueRefine: svc.runtime.EnqueueRefine, EnqueuePDFExtract: svc.runtime.EnqueuePDFExtract, StatusPending: statusPending, StatusRefiningPending: statusRefiningPending, Logf: procLogf, Errf: procErrf}.RestoreJob(),
		APIRestoreFolder:   httptransport.TrashHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, NotifyFilesChanged: svc.lifecycle.NotifyFilesChanged, Errf: procErrf}.RestoreFolder(),
		APIBatchMove:       httptransport.MoveHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, GetJob: svc.runtime.GetJob, SetJobFields: svc.runtime.SetJobFields, NotifyFilesChanged: svc.lifecycle.NotifyFilesChanged, Errf: procErrf}.BatchMove(),
		APIDownloadFolder:  httptransport.FolderDownloadHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, JobsSnapshot: jobsSnapshot, CollectFolderSubtree: collectFolderSubtree, StatusCompleted: statusCompleted, FolderSvc: svc.folderSvc, BlobSvc: svc.blobSvc}.Handler(),
		APIUpload:          uploadH.PostJSON(),
		APICreateFolder:    httptransport.FolderMutationHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, NotifyFilesChanged: svc.lifecycle.NotifyFilesChanged, Errf: procErrf}.Create(),
		APIRenameFolder:    httptransport.FolderMutationHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, NotifyFilesChanged: svc.lifecycle.NotifyFilesChanged, Errf: procErrf}.Rename(),
		APITrashFolder:     httptransport.FolderMutationHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, NotifyFilesChanged: svc.lifecycle.NotifyFilesChanged, CollectFolderSubtree: collectFolderSubtree, MarkSubtreeJobsTrashed: markSubtreeJobsTrashed, Errf: procErrf}.Trash(),
		APIRenameJob:       httptransport.JobMutationHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, GetJob: svc.runtime.GetJob, SetJobFields: svc.runtime.SetJobFields}.Rename(),
		APITrashJob:        httptransport.JobMutationHandlers{CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized, FolderSvc: svc.folderSvc, GetJob: svc.runtime.GetJob, MarkJobTrashed: svc.lifecycle.MarkTrashed, Errf: procErrf}.Trash(),
	}
}
