package httpx

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/store"
)

type JobSupportDeps struct {
	JobsSnapshot      func() map[string]*model.Job
	UploadedTS        func(string) float64
	NormalizeFolderID func(string) string
	IsJobTrashed      func(*model.Job) bool
	Fallback          func(string, string) string
	Errf              func(string, error, string, ...any)
}

func BuildJobRowsForUser(userID, q, tag, folderID string, trashed bool, deps JobSupportDeps) []JobRow {
	qNorm := norm.NFC.String(strings.ToLower(q))
	tag = strings.TrimSpace(tag)
	folderID = deps.NormalizeFolderID(folderID)

	allFolders, _ := store.ListAllFoldersByOwner(userID, false)
	folderMap := make(map[string]string)
	for _, f := range allFolders {
		folderMap[f.ID] = f.Name
	}

	snapshot := deps.JobsSnapshot()
	rows := make([]JobRow, 0, len(snapshot))
	for id, job := range snapshot {
		if job.OwnerID != userID || deps.IsJobTrashed(job) != trashed {
			continue
		}
		if !trashed && deps.NormalizeFolderID(job.FolderID) != folderID {
			continue
		}
		filename := job.Filename
		if qNorm != "" && !strings.Contains(norm.NFC.String(strings.ToLower(filename)), qNorm) {
			continue
		}
		tags := job.Tags
		if tag != "" && !containsTag(tags, tag) {
			continue
		}

		fID := deps.NormalizeFolderID(job.FolderID)
		fName := "내 파일"
		if fID != "" {
			if n, ok := folderMap[fID]; ok {
				fName = n
			}
		}

		rows = append(rows, JobRow{
			ID:              id,
			Filename:        filename,
			FileType:        job.FileType,
			MediaDuration:   deps.Fallback(job.MediaDuration, "-"),
			Status:          job.Status,
			Phase:           job.Phase,
			ProgressPercent: job.ProgressPercent,
			IsRefined:       job.IsRefined(),
			TagText:         strings.Join(tags, ", "),
			FolderID:        fID,
			IsTrashed:       deps.IsJobTrashed(job),
			UpdatedAt:       jobDisplayUpdatedAt(job),
			DeletedAt:       job.DeletedAt,
			OwnerName:       "나",
			FolderName:      fName,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return deps.UploadedTS(rows[i].ID) > deps.UploadedTS(rows[j].ID) })
	return rows
}

func BuildFolderRowsForUser(userID, folderID, q string, deps JobSupportDeps) []FolderRow {
	folderID = deps.NormalizeFolderID(folderID)
	folders, err := store.ListFoldersByParent(userID, folderID, false)
	if err != nil {
		deps.Errf("folders.listByParent", err, "owner_id=%s folder_id=%s", userID, folderID)
		return nil
	}
	qNorm := norm.NFC.String(strings.ToLower(strings.TrimSpace(q)))
	out := make([]FolderRow, 0, len(folders))
	for _, f := range folders {
		if qNorm != "" && !strings.Contains(norm.NFC.String(strings.ToLower(f.Name)), qNorm) {
			continue
		}
		out = append(out, FolderRow{ID: f.ID, Name: f.Name, ParentID: f.ParentID, UpdatedAt: f.UpdatedAt})
	}
	return out
}

func BuildRecentJobRowsForUser(userID, q, tag string, deps JobSupportDeps) []JobRow {
	qNorm := norm.NFC.String(strings.ToLower(strings.TrimSpace(q)))
	tag = strings.TrimSpace(tag)

	allFolders, _ := store.ListAllFoldersByOwner(userID, false)
	folderMap := make(map[string]string)
	for _, f := range allFolders {
		folderMap[f.ID] = f.Name
	}

	snapshot := deps.JobsSnapshot()
	rows := make([]JobRow, 0, len(snapshot))
	for id, job := range snapshot {
		if job.OwnerID != userID || deps.IsJobTrashed(job) {
			continue
		}
		filename := job.Filename
		if qNorm != "" && !strings.Contains(norm.NFC.String(strings.ToLower(filename)), qNorm) {
			continue
		}
		tags := job.Tags
		if tag != "" && !containsTag(tags, tag) {
			continue
		}

		fID := deps.NormalizeFolderID(job.FolderID)
		fName := "내 파일"
		if fID != "" {
			if n, ok := folderMap[fID]; ok {
				fName = n
			}
		}

		rows = append(rows, JobRow{
			ID:              id,
			Filename:        filename,
			FileType:        job.FileType,
			MediaDuration:   deps.Fallback(job.MediaDuration, "-"),
			Status:          job.Status,
			Phase:           job.Phase,
			ProgressPercent: job.ProgressPercent,
			IsRefined:       job.IsRefined(),
			TagText:         strings.Join(tags, ", "),
			FolderID:        fID,
			IsTrashed:       false,
			UpdatedAt:       jobDisplayUpdatedAt(job),
			DeletedAt:       job.DeletedAt,
			OwnerName:       "나",
			FolderName:      fName,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return deps.UploadedTS(rows[i].ID) > deps.UploadedTS(rows[j].ID) })
	return rows
}

func SortJobRows(rows []JobRow, sortBy, sortOrder string, uploadedTS func(string) float64) {
	desc := sortOrder == "desc"
	switch sortBy {
	case "name":
		sort.Slice(rows, func(i, j int) bool {
			a := strings.ToLower(rows[i].Filename)
			b := strings.ToLower(rows[j].Filename)
			if a == b {
				if desc {
					return uploadedTS(rows[i].ID) > uploadedTS(rows[j].ID)
				}
				return uploadedTS(rows[i].ID) < uploadedTS(rows[j].ID)
			}
			if desc {
				return a > b
			}
			return a < b
		})
	default:
		sort.Slice(rows, func(i, j int) bool {
			if desc {
				return uploadedTS(rows[i].ID) > uploadedTS(rows[j].ID)
			}
			return uploadedTS(rows[i].ID) < uploadedTS(rows[j].ID)
		})
	}
}

func SortFolderRows(rows []FolderRow, sortBy, sortOrder string) {
	desc := sortOrder == "desc"
	sort.Slice(rows, func(i, j int) bool {
		if sortBy != "name" {
			if rows[i].UpdatedAt == rows[j].UpdatedAt {
				a := strings.ToLower(rows[i].Name)
				b := strings.ToLower(rows[j].Name)
				if a == b {
					if desc {
						return rows[i].ID > rows[j].ID
					}
					return rows[i].ID < rows[j].ID
				}
				if desc {
					return a > b
				}
				return a < b
			}
			if desc {
				return rows[i].UpdatedAt > rows[j].UpdatedAt
			}
			return rows[i].UpdatedAt < rows[j].UpdatedAt
		}
		a := strings.ToLower(rows[i].Name)
		b := strings.ToLower(rows[j].Name)
		if a == b {
			if desc {
				return rows[i].ID > rows[j].ID
			}
			return rows[i].ID < rows[j].ID
		}
		if desc {
			return a > b
		}
		return a < b
	})
}

func JobsSnapshotVersion(jobItems []JobRow, folderItems []FolderRow, page, pageSize, totalPages, totalItems int) string {
	h := fnv.New64a()
	fmt.Fprintf(h, "p=%d|ps=%d|tp=%d|ti=%d;", page, pageSize, totalPages, totalItems)
	for _, f := range folderItems {
		fmt.Fprintf(h, "F|%s|%s|%s;", f.ID, f.Name, f.ParentID)
	}
	for _, j := range jobItems {
		fmt.Fprintf(
			h,
			"J|%s|%s|%s|%s|%s|%s|%d|%t|%s|%s|%t|%s|%s;",
			j.ID,
			j.Filename,
			j.FileType,
			j.MediaDuration,
			j.Status,
			j.Phase,
			j.ProgressPercent,
			j.IsRefined,
			j.TagText,
			j.FolderID,
			j.IsTrashed,
			j.UpdatedAt,
			j.DeletedAt,
		)
	}
	return fmt.Sprintf("%x", h.Sum64())
}

func RecentFolderRowsForUser(userID string) []FolderRow {
	allFolders, _ := store.ListAllFoldersByOwner(userID, false)
	sort.Slice(allFolders, func(i, j int) bool {
		return allFolders[i].UpdatedAt > allFolders[j].UpdatedAt
	})
	capacity := 4
	if len(allFolders) < capacity {
		capacity = len(allFolders)
	}
	out := make([]FolderRow, 0, capacity)
	for i := 0; i < len(allFolders) && i < 4; i++ {
		f := allFolders[i]
		out = append(out, FolderRow{ID: f.ID, Name: f.Name, ParentID: f.ParentID, UpdatedAt: f.UpdatedAt})
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
