package files

import (
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/transport/http"
	intutil "whisperserver/src/internal/util"
)

type Query struct {
	JobsSnapshot           func() map[string]*model.Job
	UploadedTS             func(string) float64
	ListAllFoldersByOwner  func(string, bool) ([]model.Folder, error)
	JobBlobUsageMapByOwner func(string) (map[string]int64, error)
	ListFoldersByParent    func(string, string, bool) ([]model.Folder, error)
	Errf                   func(string, error, string, ...any)
}

func (q Query) BuildJobRowsForUser(userID, term, tag, folderID string, trashed bool) []httptransport.JobRow {
	qNorm := norm.NFC.String(strings.ToLower(term))
	tag = strings.TrimSpace(tag)
	folderID = strings.TrimSpace(folderID)

	allFolders, _ := q.ListAllFoldersByOwner(userID, false)
	folderMap := make(map[string]string, len(allFolders))
	for _, f := range allFolders {
		folderMap[f.ID] = f.Name
	}

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

func (q Query) BuildRecentJobRowsForUser(userID, term, tag string) []httptransport.JobRow {
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

func containsTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}

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

func isJobTrashed(job *model.Job) bool {
	return job != nil && job.IsTrashed
}
