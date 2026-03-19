package app

import (
	httpx "whisperserver/src/internal/http"
	intutil "whisperserver/src/internal/util"
)

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
		NormalizeFolderID: normalizeFolderID,
		IsJobTrashed:      isJobTrashed,
		Fallback:          intutil.Fallback,
		Errf:              procErrf,
	}
}
