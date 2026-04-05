package files

import (
	"testing"

	"whisperserver/src/internal/model"
)

func TestBuildJobRowsForUserFiltersAndMapsFolder(t *testing.T) {
	q := Query{
		JobsSnapshot: func() map[string]*model.Job {
			return map[string]*model.Job{
				"j1": {OwnerID: "u1", Filename: "Lecture.mp3", FileType: "mp3", FolderID: "f1", UploadedAt: "1", UploadedTS: 10, Tags: []string{"go"}},
				"j2": {OwnerID: "u1", Filename: "Other.mp3", FileType: "mp3", FolderID: "f2", UploadedAt: "1", UploadedTS: 5},
			}
		},
		UploadedTS: func(id string) float64 { if id == "j1" { return 10 }; return 5 },
		ListAllFoldersByOwner: func(userID string, trashed bool) ([]model.Folder, error) {
			return []model.Folder{{ID: "f1", Name: "Folder A"}, {ID: "f2", Name: "Folder B"}}, nil
		},
		JobBlobUsageMapByOwner: func(userID string) (map[string]int64, error) { return map[string]int64{"j1": 100}, nil },
	}
	rows := q.BuildJobRowsForUser("u1", "lect", "go", "f1", false)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].FolderName != "Folder A" || rows[0].SizeBytes != 100 {
		t.Fatalf("unexpected row: %+v", rows[0])
	}
}

func TestRecentFolderRowsForUserSortedByUpdatedAt(t *testing.T) {
	q := Query{
		ListAllFoldersByOwner: func(userID string, trashed bool) ([]model.Folder, error) {
			return []model.Folder{
				{ID: "1", Name: "One", UpdatedAt: "2024-01-01 00:00:00"},
				{ID: "2", Name: "Two", UpdatedAt: "2024-01-03 00:00:00"},
				{ID: "3", Name: "Three", UpdatedAt: "2024-01-02 00:00:00"},
			}, nil
		},
	}
	rows := q.RecentFolderRowsForUser("u1")
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].ID != "2" || rows[1].ID != "3" || rows[2].ID != "1" {
		t.Fatalf("unexpected order: %+v", rows)
	}
}
