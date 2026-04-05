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

func jobsDeps() httpx.JobsDeps {
	return httpx.JobsDeps{
		CurrentUser:     func(c echo.Context) (*httpx.User, error) { return currentUser(c) },
		CurrentUserName: currentUserName,
		RequireOwnedJob: func(c echo.Context, id string, allow bool) (*model.Job, *httpx.User, error) {
			return requireOwnedJob(c, id, allow)
		},
		DisableCache:        httpx.DisableCache,
		NormalizeSortParams: httpx.NormalizeSortParams,
		NormalizeFolderID:   httpx.NormalizeFolderID,
		ParsePositiveInt:    httpx.ParsePositiveInt,
		PaginateRows:        paginateRows,
		BuildRecentJobRows:  buildRecentJobRowsForUser,
		BuildJobRows:        buildJobRowsForUser,
		BuildFolderRows:     buildFolderRowsForUser,
		RecentFolderRows:    recentFolderRowsForUser,
		SortFolderRows:      sortFolderRows,
		SortJobRows:         sortJobRows,
		JobsSnapshotVersion: jobsSnapshotVersion,
		SelectedTagMap:      httpx.SelectedTagMap,
		ToJobView:           toJobView,
		RenderResultText:    renderResultText,
		RenderMarkdownText:  renderMarkdownText,
		Fallback:            intutil.Fallback,
		SanitizePreviewText: sanitizePreviewText,
		HasGeminiConfigured: hasGeminiConfigured,
		SetJobFields:        setJobFields,
		EnqueueRefine:       enqueueRefine,
		GetJob:              getJob,
		IsJobTrashed:        httpx.IsJobTrashed,
		Logf:                procLogf,
		Errf:                procErrf,
	}
}

func uploadDeps() httpx.UploadDeps {
	return httpx.UploadDeps{
		CurrentUser:       func(c echo.Context) (*httpx.User, error) { return currentUser(c) },
		CurrentUserName:   currentUserName,
		ParseSelectedTags: func(c echo.Context) []string { return httpx.ParseSelectedTags(c, intutil.UniqueStringsKeepOrder) },
		NormalizeFolderID: httpx.NormalizeFolderID,
		Truthy:            intutil.Truthy,
		DetectFileType:    intutil.DetectFileType,
		AllowedFile:       func(name string) bool { return intutil.AllowedFile(name, allowedExtensions) },
		SortedExts:        func() []string { return intutil.SortedExts(allowedExtensions) },
		SecureFilename:    func(name string) string { return intutil.SecureFilename(name, secureRe) },
		SaveUploadWithLimit: func(h *multipart.FileHeader, dst string, maxBytes int64, bytesPerSec int64) (int64, error) {
			return intutil.SaveUploadWithLimit(h, dst, maxBytes, chunkSize, bytesPerSec)
		},
		IsUploadTooLarge:    func(err error) bool { return errors.Is(err, intutil.ErrUploadTooLarge) },
		ConvertToAac:        intutil.ConvertToAac,
		GetMediaDuration:    intutil.GetMediaDuration,
		FormatSecondsPtr:    intutil.FormatSecondsPtr,
		AddJob:              addJob,
		DeleteJobs:          deleteJobs,
		SetJobFields:        setJobFields,
		EnqueueTranscribe:   enqueueTranscribe,
		EnqueuePDFExtract:   enqueuePDFExtract,
		Logf:                procLogf,
		Errf:                procErrf,
		UploadBytesAdd:      uploadBytes.Add,
		TmpFolder:           tmpFolder,
		MaxUploadSizeMB:     maxUploadSizeMB,
		UploadRateLimitKBPS: uploadRateLimitKB,
		StatusPending:       statusPending,
		StatusFailed:        statusFailed,
	}
}

func tagDeps() httpx.TagDeps {
	return httpx.TagDeps{
		CurrentUser:            func(c echo.Context) (*httpx.User, error) { return currentUser(c) },
		CurrentUserName:        currentUserName,
		GetJob:                 getJob,
		SetJobFields:           setJobFields,
		ParseSelectedTags:      func(c echo.Context) []string { return httpx.ParseSelectedTags(c, intutil.UniqueStringsKeepOrder) },
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
		IsJobTrashed:      httpx.IsJobTrashed,
		NormalizeFolderID: httpx.NormalizeFolderID,
		SafeReturnPath:    httpx.SafeReturnPath,
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
		IsJobTrashed:           httpx.IsJobTrashed,
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
		EnqueuePDFExtract:      enqueuePDFExtract,
		HasAudioBlob:           func(jobID string) bool { return store.HasJobBlob(jobID, store.BlobKindAudioAAC) },
		HasJobBlob:             store.HasJobBlob,
		StatusPending:          statusPending,
		StatusRunning:          statusRunning,
		StatusRefiningPending:  statusRefiningPending,
		StatusRefining:         statusRefining,
		IsJobTrashed:           httpx.IsJobTrashed,
		NormalizeFolderID:      httpx.NormalizeFolderID,
		CollectFolderSubtree:   collectFolderSubtree,
		MarkSubtreeJobsTrashed: markSubtreeJobsTrashed,
		Logf:                   procLogf,
		Errf:                   procErrf,
	}
}

func buildJobRowsForUser(userID, q, tag, folderID string, trashed bool) []JobRow {
	return httpx.BuildJobRowsForUser(userID, q, tag, folderID, trashed, jobSupportDeps())
}

func buildFolderRowsForUser(userID, folderID, q string) []FolderRow {
	return httpx.BuildFolderRowsForUser(userID, folderID, q, jobSupportDeps())
}

func buildRecentJobRowsForUser(userID, q, tag string) []JobRow {
	return httpx.BuildRecentJobRowsForUser(userID, q, tag, jobSupportDeps())
}

func sortJobRows(rows []JobRow, sortBy, sortOrder string) {
	httpx.SortJobRows(rows, sortBy, sortOrder, uploadedTS)
}

func sortFolderRows(rows []FolderRow, sortBy, sortOrder string) {
	httpx.SortFolderRows(rows, sortBy, sortOrder)
}

func jobsSnapshotVersion(jobItems []JobRow, folderItems []FolderRow, page, pageSize, totalPages, totalItems int) string {
	return httpx.JobsSnapshotVersion(jobItems, folderItems, page, pageSize, totalPages, totalItems)
}

func recentFolderRowsForUser(userID string) []FolderRow {
	return httpx.RecentFolderRowsForUser(userID)
}

func jobSupportDeps() httpx.JobSupportDeps {
	return httpx.JobSupportDeps{
		JobsSnapshot:      jobsSnapshot,
		UploadedTS:        uploadedTS,
		BlobUsageByOwner:  store.JobBlobUsageMapByOwner,
		NormalizeFolderID: httpx.NormalizeFolderID,
		IsJobTrashed:      httpx.IsJobTrashed,
		Fallback:          intutil.Fallback,
		Errf:              procErrf,
	}
}
