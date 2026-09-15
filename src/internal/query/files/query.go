package files

import (
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	model "whisperserver/src/internal/domain"
	store "whisperserver/src/internal/repo/sqlite"
	"whisperserver/src/internal/transport/http"
	intutil "whisperserver/src/internal/util"
)

// PagedJobQueryParams defines query filter, sort, and pagination parameters.
type PagedJobQueryParams = httptransport.PagedJobQueryParams

// PagedJobRowsResult bundles the page of rows with total counts.
type PagedJobRowsResult = httptransport.PagedJobRowsResult

// Query builds file-browser row models from runtime snapshots and repository data.
type Query struct {
	JobsSnapshot           func() map[string]*model.Job
	GetActiveJob           func(string) *model.Job
	QueryJobsPaged         func(store.JobQueryFilter) (*store.PagedJobsResult, error)
	JobBlobUsageMapForJobs func([]string) (map[string]int64, error)
	UploadedTS             func(string) float64
	ListAllFoldersByOwner  func(string, bool) ([]model.Folder, error)
	JobBlobUsageMapByOwner func(string) (map[string]int64, error)
	ListFoldersByParent    func(string, string, bool) ([]model.Folder, error)
	Errf                   func(string, error, string, ...any)
}

// BuildPagedJobRowsForUser executes a database-level paginated query and applies the Active Job Overlay.
func (q Query) BuildPagedJobRowsForUser(params PagedJobQueryParams) (PagedJobRowsResult, error) {
	if q.QueryJobsPaged != nil {
		res, err := q.QueryJobsPaged(store.JobQueryFilter{
			OwnerID:      params.UserID,
			FolderID:     params.FolderID,
			FilterFolder: params.FilterFolder,
			IsTrashed:    params.IsTrashed,
			SearchQuery:  params.SearchQuery,
			Tag:          params.Tag,
			SortBy:       params.SortBy,
			SortOrder:    params.SortOrder,
			Page:         params.Page,
			PageSize:     params.PageSize,
		})
		if err != nil {
			if q.Errf != nil {
				q.Errf("query.jobsPaged", err, "user=%s", params.UserID)
			}
			return PagedJobRowsResult{}, err
		}

		allFolders, _ := q.ListAllFoldersByOwner(params.UserID, false)
		folderMap := make(map[string]string, len(allFolders))
		for _, f := range allFolders {
			folderMap[f.ID] = f.Name
		}

		jobIDs := make([]string, len(res.Records))
		for i, rec := range res.Records {
			jobIDs[i] = rec.ID
		}

		var sizeMap map[string]int64
		if q.JobBlobUsageMapForJobs != nil {
			sizeMap, _ = q.JobBlobUsageMapForJobs(jobIDs)
		} else if q.JobBlobUsageMapByOwner != nil {
			sizeMap, _ = q.JobBlobUsageMapByOwner(params.UserID)
		}

		rows := make([]httptransport.JobRow, 0, len(res.Records))
		for _, rec := range res.Records {
			id := rec.ID
			job := rec.Job
			fID := strings.TrimSpace(job.FolderID)
			fName := "내 파일"
			if fID != "" {
				if n, ok := folderMap[fID]; ok {
					fName = n
				}
			}
			durStr := "-"
			if job.MediaDurationSeconds != nil {
				durStr = intutil.FormatSeconds(*job.MediaDurationSeconds)
			} else if job.MediaDuration != "" {
				durStr = job.MediaDuration
			}
			phase := job.Phase
			if phase == "" {
				phase = model.JobPhase(job.StatusCode)
			}

			row := httptransport.JobRow{
				ID:              id,
				Filename:        job.Filename,
				FileType:        job.FileType,
				MediaDuration:   durStr,
				SizeBytes:       sizeMap[id],
				StatusCode:      job.StatusCode,
				Status:          job.Status,
				Phase:           phase,
				ProgressPercent: job.ProgressPercent,
				StatusDetail:    job.StatusDetail,
				IsRefined:       job.IsRefined(),
				TagText:         strings.Join(job.Tags, ", "),
				FolderID:        fID,
				ClientUploadID:  job.ClientUploadID,
				IsTrashed:       isJobTrashed(job),
				UpdatedAt:       jobDisplayUpdatedAt(job),
				DeletedAt:       job.DeletedAt,
				OwnerName:       "나",
				FolderName:      fName,
			}

			// ACTIVE JOB OVERLAY:
			// Real-time in-flight progress & active status from the coordinator
			if q.GetActiveJob != nil {
				if active := q.GetActiveJob(id); active != nil {
					row.StatusCode = active.StatusCode
					row.Status = active.Status
					if active.Phase != "" {
						row.Phase = active.Phase
					}
					row.ProgressPercent = active.ProgressPercent
					if active.StatusDetail != "" {
						row.StatusDetail = active.StatusDetail
					}
					row.IsRefined = active.IsRefined()
				}
			}

			rows = append(rows, row)
		}

		return PagedJobRowsResult{
			Rows:       rows,
			TotalItems: res.TotalItems,
			TotalPages: res.TotalPages,
			Page:       res.Page,
			PageSize:   res.PageSize,
		}, nil
	}

	// Fallback to in-memory scanning if QueryJobsPaged is not provided
	var rows []httptransport.JobRow
	if params.FilterFolder {
		rows = q.BuildJobRowsForUser(params.UserID, params.SearchQuery, params.Tag, params.FolderID, params.IsTrashed)
	} else {
		rows = q.BuildRecentJobRowsForUser(params.UserID, params.SearchQuery, params.Tag)
	}
	if q.UploadedTS != nil {
		httptransport.SortJobRows(rows, params.SortBy, params.SortOrder, q.UploadedTS)
	}
	paged, page, totalPages := httptransport.PaginateJobRows(rows, params.Page, params.PageSize)
	return PagedJobRowsResult{
		Rows:       paged,
		TotalItems: len(rows),
		TotalPages: totalPages,
		Page:       page,
		PageSize:   params.PageSize,
	}, nil
}

// BuildJobRowsForUser returns folder-scoped job rows for the files browser.
func (q Query) BuildJobRowsForUser(userID, term, tag, folderID string, trashed bool) []httptransport.JobRow {
	if q.QueryJobsPaged != nil {
		filterFolder := folderID != "" || !trashed
		pagedRes, err := q.BuildPagedJobRowsForUser(PagedJobQueryParams{
			UserID:       userID,
			FolderID:     folderID,
			FilterFolder: filterFolder,
			IsTrashed:    trashed,
			SearchQuery:  term,
			Tag:          tag,
			Page:         1,
			PageSize:     10000,
		})
		if err == nil {
			return pagedRes.Rows
		}
	}

	if q.JobsSnapshot == nil {
		return nil
	}

	qNorm := norm.NFC.String(strings.ToLower(term))
	tag = strings.TrimSpace(tag)
	folderID = strings.TrimSpace(folderID)

	allFolders, _ := q.ListAllFoldersByOwner(userID, false)
	folderMap := make(map[string]string, len(allFolders))
	for _, f := range allFolders {
		folderMap[f.ID] = f.Name
	}

	// Combine in-memory job state with persisted blob usage and folder metadata.
	snapshot := q.JobsSnapshot()
	sizeMap, _ := q.JobBlobUsageMapByOwner(userID)
	rows := make([]httptransport.JobRow, 0, len(snapshot))
	for id, job := range snapshot {
		if job.OwnerID != userID || isJobTrashed(job) != trashed {
			continue
		}
		if !trashed && strings.TrimSpace(job.FolderID) != folderID {
			continue
		}
		filename := job.Filename
		if qNorm != "" && !strings.Contains(norm.NFC.String(strings.ToLower(filename)), qNorm) {
			continue
		}
		if tag != "" && !containsTag(job.Tags, tag) {
			continue
		}

		fID := strings.TrimSpace(job.FolderID)
		fName := "내 파일"
		if fID != "" {
			if n, ok := folderMap[fID]; ok {
				fName = n
			}
		}

		rows = append(rows, httptransport.JobRow{
			ID:              id,
			Filename:        filename,
			FileType:        job.FileType,
			MediaDuration:   intutil.Fallback(job.MediaDuration, "-"),
			SizeBytes:       sizeMap[id],
			StatusCode:      job.StatusCode,
			Status:          job.Status,
			Phase:           job.Phase,
			ProgressPercent: job.ProgressPercent,
			StatusDetail:    job.StatusDetail,
			IsRefined:       job.IsRefined(),
			TagText:         strings.Join(job.Tags, ", "),
			FolderID:        fID,
			ClientUploadID:  job.ClientUploadID,
			IsTrashed:       isJobTrashed(job),
			UpdatedAt:       jobDisplayUpdatedAt(job),
			DeletedAt:       job.DeletedAt,
			OwnerName:       "나",
			FolderName:      fName,
		})
	}
	httptransport.SortJobRows(rows, "", "desc", q.UploadedTS)
	return rows
}

// BuildFolderRowsForUser returns child folders for the current folder view.
func (q Query) BuildFolderRowsForUser(userID, folderID, term string) []httptransport.FolderRow {
	folderID = strings.TrimSpace(folderID)
	folders, err := q.ListFoldersByParent(userID, folderID, false)
	if err != nil {
		if q.Errf != nil {
			q.Errf("folders.listByParent", err, "owner_id=%s folder_id=%s", userID, folderID)
		}
		return nil
	}
	qNorm := norm.NFC.String(strings.ToLower(strings.TrimSpace(term)))
	out := make([]httptransport.FolderRow, 0, len(folders))
	for _, f := range folders {
		if qNorm != "" && !strings.Contains(norm.NFC.String(strings.ToLower(f.Name)), qNorm) {
			continue
		}
		out = append(out, httptransport.FolderRow{ID: f.ID, Name: f.Name, ParentID: f.ParentID, UpdatedAt: f.UpdatedAt})
	}
	return out
}

// BuildRecentJobRowsForUser returns recent jobs across every folder.
func (q Query) BuildRecentJobRowsForUser(userID, term, tag string) []httptransport.JobRow {
	if q.QueryJobsPaged != nil {
		pagedRes, err := q.BuildPagedJobRowsForUser(PagedJobQueryParams{
			UserID:       userID,
			FilterFolder: false,
			IsTrashed:    false,
			SearchQuery:  term,
			Tag:          tag,
			Page:         1,
			PageSize:     100,
		})
		if err == nil {
			return pagedRes.Rows
		}
	}

	if q.JobsSnapshot == nil {
		return nil
	}

	qNorm := norm.NFC.String(strings.ToLower(strings.TrimSpace(term)))
	tag = strings.TrimSpace(tag)

	allFolders, _ := q.ListAllFoldersByOwner(userID, false)
	folderMap := make(map[string]string, len(allFolders))
	for _, f := range allFolders {
		folderMap[f.ID] = f.Name
	}

	snapshot := q.JobsSnapshot()
	sizeMap, _ := q.JobBlobUsageMapByOwner(userID)
	rows := make([]httptransport.JobRow, 0, len(snapshot))
	for id, job := range snapshot {
		if job.OwnerID != userID || isJobTrashed(job) {
			continue
		}
		filename := job.Filename
		if qNorm != "" && !strings.Contains(norm.NFC.String(strings.ToLower(filename)), qNorm) {
			continue
		}
		if tag != "" && !containsTag(job.Tags, tag) {
			continue
		}

		fID := strings.TrimSpace(job.FolderID)
		fName := "내 파일"
		if fID != "" {
			if n, ok := folderMap[fID]; ok {
				fName = n
			}
		}

		rows = append(rows, httptransport.JobRow{
			ID:              id,
			Filename:        filename,
			FileType:        job.FileType,
			MediaDuration:   intutil.Fallback(job.MediaDuration, "-"),
			SizeBytes:       sizeMap[id],
			StatusCode:      job.StatusCode,
			Status:          job.Status,
			Phase:           job.Phase,
			ProgressPercent: job.ProgressPercent,
			StatusDetail:    job.StatusDetail,
			IsRefined:       job.IsRefined(),
			TagText:         strings.Join(job.Tags, ", "),
			FolderID:        fID,
			ClientUploadID:  job.ClientUploadID,
			IsTrashed:       false,
			UpdatedAt:       jobDisplayUpdatedAt(job),
			DeletedAt:       job.DeletedAt,
			OwnerName:       "나",
			FolderName:      fName,
		})
	}
	httptransport.SortJobRows(rows, "", "desc", q.UploadedTS)
	return rows
}

// RecentFolderRowsForUser returns a small recent-folder set for the home view.
func (q Query) RecentFolderRowsForUser(userID string) []httptransport.FolderRow {
	allFolders, _ := q.ListAllFoldersByOwner(userID, false)
	sort.Slice(allFolders, func(i, j int) bool { return allFolders[i].UpdatedAt > allFolders[j].UpdatedAt })
	capacity := 4
	if len(allFolders) < capacity {
		capacity = len(allFolders)
	}
	out := make([]httptransport.FolderRow, 0, capacity)
	for i := 0; i < len(allFolders) && i < 4; i++ {
		f := allFolders[i]
		out = append(out, httptransport.FolderRow{ID: f.ID, Name: f.Name, ParentID: f.ParentID, UpdatedAt: f.UpdatedAt})
	}
	return out
}

// containsTag reports whether the exact tag exists in the slice.
func containsTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}

// jobDisplayUpdatedAt chooses the most meaningful timestamp for list ordering and display.
func jobDisplayUpdatedAt(job *model.Job) string {
	if job == nil {
		return ""
	}
	if strings.TrimSpace(job.CompletedAt) != "" {
		return job.CompletedAt
	}
	if strings.TrimSpace(job.StartedAt) != "" {
		return job.StartedAt
	}
	return job.UploadedAt
}

// isJobTrashed guards nil jobs while checking trash state.
func isJobTrashed(job *model.Job) bool {
	return job != nil && job.IsTrashed
}
