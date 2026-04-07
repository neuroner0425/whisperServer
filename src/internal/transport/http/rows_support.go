package httptransport

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// SortJobRows orders job rows for list responses and legacy pages.
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

// SortFolderRows orders folder rows using the same sort inputs as jobs.
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

// JobsSnapshotVersion hashes list payloads so clients can skip unchanged refreshes.
func JobsSnapshotVersion(jobItems []JobRow, folderItems []FolderRow, page, pageSize, totalPages, totalItems int) string {
	h := fnv.New64a()
	fmt.Fprintf(h, "p=%d|ps=%d|tp=%d|ti=%d;", page, pageSize, totalPages, totalItems)
	for _, f := range folderItems {
		fmt.Fprintf(h, "F|%s|%s|%s;", f.ID, f.Name, f.ParentID)
	}
	for _, j := range jobItems {
		fmt.Fprintf(
			h,
			"J|%s|%s|%s|%s|%d|%s|%s|%d|%s|%t|%s|%s|%s|%t|%s|%s;",
			j.ID,
			j.Filename,
			j.FileType,
			j.MediaDuration,
			j.SizeBytes,
			j.Status,
			j.Phase,
			j.ProgressPercent,
			j.StatusDetail,
			j.IsRefined,
			j.TagText,
			j.FolderID,
			j.ClientUploadID,
			j.IsTrashed,
			j.UpdatedAt,
			j.DeletedAt,
		)
	}
	return fmt.Sprintf("%x", h.Sum64())
}
