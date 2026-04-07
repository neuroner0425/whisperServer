package server

import (
	"strings"

	"github.com/labstack/echo/v4"

	filequery "whisperserver/src/internal/query/files"
	store "whisperserver/src/internal/repo/sqlite"
	httptransport "whisperserver/src/internal/transport/http"
	intutil "whisperserver/src/internal/util"
)

// newFilesQuery builds the query adapter used by file-list handlers.
func newFilesQuery() filequery.Query {
	return filequery.Query{
		JobsSnapshot:           jobsSnapshot,
		UploadedTS:             appRuntime.UploadedTS,
		ListAllFoldersByOwner:  store.ListAllFoldersByOwner,
		JobBlobUsageMapByOwner: store.JobBlobUsageMapByOwner,
		ListFoldersByParent:    store.ListFoldersByParent,
		Errf:                   procErrf,
	}
}

// newUploadHandlers wires upload transport handlers to shared helpers and services.
func newUploadHandlers(svc appServices) httptransport.UploadHandlers {
	return httptransport.UploadHandlers{
		CurrentUser:               transportCurrentUser,
		CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized,
		ParseSelectedTags: func(c echo.Context) []string {
			return httptransport.ParseSelectedTags(c, intutil.UniqueStringsKeepOrder)
		},
		NormalizeFolderID: httptransport.NormalizeFolderID,
		Truthy:            intutil.Truthy,
		Svc:               svc.uploadSvc,
	}
}

// newLegacyJobsHandlers wires legacy polling and download handlers.
func newLegacyJobsHandlers(svc appServices, filesQ filequery.Query) httptransport.LegacyJobsHandlers {
	return httptransport.LegacyJobsHandlers{
		CurrentUserOrUnauthorized: transportCurrentUserOrUnauthorized,
		FolderSvc:                 svc.folderSvc,
		BlobSvc:                   svc.blobSvc,
		BuildRecentJobRows:        filesQ.BuildRecentJobRowsForUser,
		BuildJobRows:              filesQ.BuildJobRowsForUser,
		BuildFolderRows:           filesQ.BuildFolderRowsForUser,
		RecentFolderRows:          filesQ.RecentFolderRowsForUser,
		SortJobRows: func(rows []httptransport.JobRow, sortBy, sortOrder string) {
			httptransport.SortJobRows(rows, sortBy, sortOrder, svc.runtime.UploadedTS)
		},
		SortFolderRows:  httptransport.SortFolderRows,
		PaginateRows:    httptransport.PaginateJobRows,
		SnapshotVersion: httptransport.JobsSnapshotVersion,
		GetJob:          svc.runtime.GetJob,
		StatusCompleted: statusCompleted,
		Logf:            procLogf,
		Errf:            procErrf,
	}
}

// newLegacyRefineHandlers wires the legacy refine retry endpoint.
func newLegacyRefineHandlers(svc appServices) httptransport.LegacyRefineHandlers {
	return httptransport.LegacyRefineHandlers{
		CurrentUser:           transportCurrentUser,
		GetJob:                svc.runtime.GetJob,
		BlobSvc:               svc.blobSvc,
		SetJobFields:          svc.runtime.SetJobFields,
		EnqueueRefine:         svc.runtime.EnqueueRefine,
		HasGeminiConfigured:   svc.geminiRt.HasConfigured,
		StatusCompleted:       statusCompleted,
		StatusRefiningPending: statusRefiningPending,
		Logf:                  procLogf,
	}
}

// newLegacyMutationHandlers wires legacy HTML mutation handlers.
func newLegacyMutationHandlers(svc appServices) httptransport.LegacyMutationHandlers {
	return httptransport.LegacyMutationHandlers{
		CurrentUser:            transportCurrentUser,
		GetJob:                 svc.runtime.GetJob,
		SetJobFields:           svc.runtime.SetJobFields,
		MarkJobTrashed:         svc.lifecycle.MarkTrashed,
		FolderSvc:              svc.folderSvc,
		BlobSvc:                svc.blobSvc,
		TagSvc:                 svc.tagSvc,
		CollectFolderSubtree:   collectFolderSubtree,
		MarkSubtreeJobsTrashed: markSubtreeJobsTrashed,
		EnqueueTranscribe:      svc.runtime.EnqueueTranscribe,
		EnqueueRefine:          svc.runtime.EnqueueRefine,
		EnqueuePDFExtract:      svc.runtime.EnqueuePDFExtract,
		StatusPending:          statusPending,
		StatusRefiningPending:  statusRefiningPending,
		IsValidTagName:         intutil.IsValidTagName,
		RemoveTagFromOwnerJobs: svc.runtime.RemoveTagFromOwnerJobs,
		Logf:                   procLogf,
		Errf:                   procErrf,
	}
}

// newSPAHandlers wires SPA redirects and the SPA entrypoint.
func newSPAHandlers() httptransport.SPAHandlers {
	return httptransport.SPAHandlers{
		SPAIndexPath: spaIndexPath,
		CurrentUser:  func(c echo.Context) error { _, err := currentUser(c); return err },
	}
}

// newEventsHandler builds the authenticated SSE handler.
func newEventsHandler() echo.HandlerFunc {
	return httptransport.SSEHandlers{
		Broker: eventBroker(),
		CurrentUserOrUnauthorized: func(c echo.Context) (string, bool) {
			u, err := currentUserOrUnauthorized(c)
			if err != nil || u == nil || strings.TrimSpace(u.ID) == "" {
				return "", false
			}
			return u.ID, true
		},
	}.EventsHandler()
}
