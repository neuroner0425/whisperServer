package app

import (
	"errors"
	"mime/multipart"

	"github.com/labstack/echo/v4"
	httpx "whisperserver/src/internal/http"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/store"
	intutil "whisperserver/src/internal/util"
)

func jobsHandler(c echo.Context) error               { return httpx.JobsHandler(c, jobsDeps()) }
func jobsUpdatesHandler(c echo.Context) error        { return httpx.JobsUpdatesHandler(c, jobsDeps()) }
func apiMeHandler(c echo.Context) error              { return apiMeJSONHandler(c) }
func apiFilesHandler(c echo.Context) error           { return apiFilesJSONHandler(c) }
func apiEventsStreamHandler(c echo.Context) error    { return apiEventsHandler(c) }
func apiJobDetailHandler(c echo.Context) error       { return apiJobDetailJSONHandler(c) }
func apiTagsHandler(c echo.Context) error            { return apiTagsJSONHandler(c) }
func apiCreateTagHandler(c echo.Context) error       { return apiCreateTagJSONHandler(c) }
func apiDeleteTagHandler(c echo.Context) error       { return apiDeleteTagJSONHandler(c) }
func apiUpdateJobTagsHandler(c echo.Context) error   { return apiUpdateJobTagsJSONHandler(c) }
func apiTrashListHandler(c echo.Context) error       { return apiTrashListJSONHandler(c) }
func apiRestoreJobHandler(c echo.Context) error      { return apiRestoreJobJSONHandler(c) }
func apiRestoreFolderHandler(c echo.Context) error   { return apiRestoreFolderJSONHandler(c) }
func apiBatchMoveHandler(c echo.Context) error       { return apiBatchMoveJSONHandler(c) }
func apiClearTrashHandler(c echo.Context) error      { return clearTrashJSONHandler(c) }
func apiDeleteTrashJobsHandler(c echo.Context) error { return deleteTrashJobsJSONHandler(c) }
func apiDownloadFolderHandler(c echo.Context) error  { return downloadFolderJSONHandler(c) }
func trashHandler(c echo.Context) error              { return httpx.TrashPageHandler(c, jobsDeps()) }
func statusHandler(c echo.Context) error             { return httpx.StatusHandler(c, jobsDeps()) }
func jobHandler(c echo.Context) error                { return httpx.JobHandler(c, jobsDeps()) }
func downloadHandler(c echo.Context) error           { return httpx.DownloadHandler(c, jobsDeps()) }
func downloadRefinedHandler(c echo.Context) error    { return httpx.DownloadRefinedHandler(c, jobsDeps()) }
func batchDownloadHandler(c echo.Context) error      { return httpx.BatchDownloadHandler(c, jobsDeps()) }
func refineRetryHandler(c echo.Context) error        { return httpx.RefineRetryHandler(c, jobsDeps()) }
func uploadGetHandler(c echo.Context) error          { return httpx.UploadGetHandler(c, uploadDeps()) }
func uploadPostHandler(c echo.Context) error         { return httpx.UploadPostHandler(c, uploadDeps()) }
func createTagHandler(c echo.Context) error          { return httpx.CreateTagHandler(c, tagDeps()) }
func tagsPageHandler(c echo.Context) error           { return httpx.TagsPageHandler(c, tagDeps()) }
func deleteTagHandler(c echo.Context) error          { return httpx.DeleteTagHandler(c, tagDeps()) }
func updateJobTagsHandler(c echo.Context) error      { return httpx.UpdateJobTagsHandler(c, tagDeps()) }
func createFolderHandler(c echo.Context) error       { return httpx.CreateFolderHandler(c, folderDeps()) }
func moveJobsHandler(c echo.Context) error           { return httpx.MoveJobsHandler(c, folderDeps()) }
func batchDeleteHandler(c echo.Context) error        { return httpx.BatchDeleteHandler(c, mutationDeps()) }
func restoreJobHandler(c echo.Context) error         { return httpx.RestoreJobHandler(c, trashDeps()) }
func trashJobHandler(c echo.Context) error           { return httpx.TrashJobHandler(c, trashDeps()) }
func renameJobHandler(c echo.Context) error          { return httpx.RenameJobHandler(c, trashDeps()) }
func restoreFolderHandler(c echo.Context) error      { return httpx.RestoreFolderHandler(c, trashDeps()) }
func trashFolderHandler(c echo.Context) error        { return httpx.TrashFolderHandler(c, trashDeps()) }
func renameFolderHandler(c echo.Context) error       { return httpx.RenameFolderHandler(c, folderDeps()) }
func moveFolderHandler(c echo.Context) error         { return httpx.MoveFolderHandler(c, folderDeps()) }

func jobsDeps() httpx.JobsDeps {
	return httpx.JobsDeps{
		CurrentUser:     func(c echo.Context) (*httpx.User, error) { return currentUser(c) },
		CurrentUserName: currentUserName,
		RequireOwnedJob: func(c echo.Context, id string, allow bool) (*model.Job, *httpx.User, error) {
			return requireOwnedJob(c, id, allow)
		},
		DisableCache:        disableCache,
		NormalizeSortParams: normalizeSortParams,
		NormalizeFolderID:   normalizeFolderID,
		ParsePositiveInt:    parsePositiveInt,
		PaginateRows:        paginateRows,
		BuildRecentJobRows:  buildRecentJobRowsForUser,
		BuildJobRows:        buildJobRowsForUser,
		BuildFolderRows:     buildFolderRowsForUser,
		RecentFolderRows:    recentFolderRowsForUser,
		SortFolderRows:      sortFolderRows,
		SortJobRows:         sortJobRows,
		JobsSnapshotVersion: jobsSnapshotVersion,
		SelectedTagMap:      selectedTagMap,
		ToJobView:           toJobView,
		RenderResultText:    renderResultText,
		Fallback:            intutil.Fallback,
		SanitizePreviewText: sanitizePreviewText,
		HasGeminiConfigured: hasGeminiConfigured,
		SetJobFields:        setJobFields,
		EnqueueRefine:       enqueueRefine,
		GetJob:              getJob,
		IsJobTrashed:        isJobTrashed,
		Logf:                procLogf,
		Errf:                procErrf,
	}
}

func uploadDeps() httpx.UploadDeps {
	return httpx.UploadDeps{
		CurrentUser:       func(c echo.Context) (*httpx.User, error) { return currentUser(c) },
		CurrentUserName:   currentUserName,
		ParseSelectedTags: parseSelectedTags,
		NormalizeFolderID: normalizeFolderID,
		Truthy:            intutil.Truthy,
		DetectFileType:    intutil.DetectFileType,
		AllowedFile:       func(name string) bool { return intutil.AllowedFile(name, allowedExtensions) },
		SortedExts:        func() []string { return intutil.SortedExts(allowedExtensions) },
		SecureFilename:    func(name string) string { return intutil.SecureFilename(name, secureRe) },
		SaveUploadWithLimit: func(h *multipart.FileHeader, dst string, maxBytes int64, bytesPerSec int64) (int64, error) {
			return intutil.SaveUploadWithLimit(h, dst, maxBytes, chunkSize, bytesPerSec)
		},
		IsUploadTooLarge:    func(err error) bool { return errors.Is(err, intutil.ErrUploadTooLarge) },
		ConvertToWav:        intutil.ConvertToWav,
		GetMediaDuration:    intutil.GetMediaDuration,
		FormatSecondsPtr:    intutil.FormatSecondsPtr,
		AddJob:              addJob,
		DeleteJobs:          deleteJobs,
		EnqueueTranscribe:   enqueueTranscribe,
		Logf:                procLogf,
		Errf:                procErrf,
		UploadBytesAdd:      uploadBytes.Add,
		TmpFolder:           tmpFolder,
		MaxUploadSizeMB:     maxUploadSizeMB,
		UploadRateLimitKBPS: uploadRateLimitKB,
		StatusPending:       statusPending,
	}
}

func tagDeps() httpx.TagDeps {
	return httpx.TagDeps{
		CurrentUser:            func(c echo.Context) (*httpx.User, error) { return currentUser(c) },
		CurrentUserName:        currentUserName,
		GetJob:                 getJob,
		SetJobFields:           setJobFields,
		ParseSelectedTags:      parseSelectedTags,
		IsValidTagName:         intutil.IsValidTagName,
		RemoveTagFromOwnerJobs: removeTagFromOwnerJobs,
		Logf:                   procLogf,
		Errf:                   procErrf,
	}
}

func folderDeps() httpx.FolderDeps {
	return httpx.FolderDeps{
		CurrentUser:       func(c echo.Context) (*httpx.User, error) { return currentUser(c) },
		GetJob:            getJob,
		SetJobFields:      setJobFields,
		IsJobTrashed:      isJobTrashed,
		NormalizeFolderID: normalizeFolderID,
		SafeReturnPath:    safeReturnPath,
		Logf:              procLogf,
		Errf:              procErrf,
	}
}

func mutationDeps() httpx.MutationDeps {
	return httpx.MutationDeps{
		CurrentUser:            func(c echo.Context) (*httpx.User, error) { return currentUser(c) },
		GetJob:                 getJob,
		SetJobFields:           setJobFields,
		CancelJob:              cancelJob,
		IsJobTrashed:           isJobTrashed,
		CollectFolderSubtree:   collectFolderSubtree,
		MarkSubtreeJobsTrashed: markSubtreeJobsTrashed,
		Logf:                   procLogf,
		Errf:                   procErrf,
	}
}

func trashDeps() httpx.TrashDeps {
	return httpx.TrashDeps{
		CurrentUser:            func(c echo.Context) (*httpx.User, error) { return currentUser(c) },
		GetJob:                 getJob,
		SetJobFields:           setJobFields,
		CancelJob:              cancelJob,
		EnqueueTranscribe:      enqueueTranscribe,
		EnqueueRefine:          enqueueRefine,
		HasJobBlob:             store.HasJobBlob,
		StatusPending:          statusPending,
		StatusRunning:          statusRunning,
		StatusRefiningPending:  statusRefiningPending,
		StatusRefining:         statusRefining,
		IsJobTrashed:           isJobTrashed,
		NormalizeFolderID:      normalizeFolderID,
		CollectFolderSubtree:   collectFolderSubtree,
		MarkSubtreeJobsTrashed: markSubtreeJobsTrashed,
		Logf:                   procLogf,
		Errf:                   procErrf,
	}
}
