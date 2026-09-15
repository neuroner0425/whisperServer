package files

import (
	"testing"

	model "whisperserver/src/internal/domain"
	store "whisperserver/src/internal/repo/sqlite"
)

func TestBuildJobRowsForUserFiltersAndMapsFolder(t *testing.T) {
	q := Query{
		JobsSnapshot: func() map[string]*model.Job {
			return map[string]*model.Job{
				"j1": {OwnerID: "u1", Filename: "Lecture.mp3", FileType: "mp3", FolderID: "f1", UploadedAt: "1", UploadedTS: 10, Tags: []string{"go"}},
				"j2": {OwnerID: "u1", Filename: "Other.mp3", FileType: "mp3", FolderID: "f2", UploadedAt: "1", UploadedTS: 5},
			}
		},
		UploadedTS: func(id string) float64 {
			if id == "j1" {
				return 10
			}
			return 5
		},
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

func TestBuildPagedJobRowsForUserWithActiveOverlay(t *testing.T) {
	q := Query{
		QueryJobsPaged: func(filter store.JobQueryFilter) (*store.PagedJobsResult, error) {
			return &store.PagedJobsResult{
				Records: []store.JobRecord{
					{
						ID: "j1",
						Job: &model.Job{
							OwnerID:         "u1",
							Filename:        "running.mp3",
							StatusCode:      model.JobStatusRunningCode,
							Status:          model.JobStatusName(model.JobStatusRunningCode),
							ProgressPercent: 0, // DB has 0 because volatile progress is not persisted
						},
					},
					{
						ID: "j2",
						Job: &model.Job{
							OwnerID:         "u1",
							Filename:        "completed.mp3",
							StatusCode:      model.JobStatusCompletedCode,
							Status:          model.JobStatusName(model.JobStatusCompletedCode),
							ProgressPercent: 100,
						},
					},
				},
				TotalItems: 2,
				TotalPages: 1,
				Page:       1,
				PageSize:   10,
			}, nil
		},
		GetActiveJob: func(id string) *model.Job {
			if id == "j1" {
				return &model.Job{
					StatusCode:      model.JobStatusRunningCode,
					Status:          "작업 중",
					Phase:           "음성 전사 중 (2단계)",
					ProgressPercent: 72,
					StatusDetail:    "전사 진행 중...",
				}
			}
			return nil
		},
		JobBlobUsageMapForJobs: func(ids []string) (map[string]int64, error) {
			return map[string]int64{"j1": 500, "j2": 1500}, nil
		},
		ListAllFoldersByOwner: func(userID string, trashed bool) ([]model.Folder, error) {
			return []model.Folder{}, nil
		},
	}

	res, err := q.BuildPagedJobRowsForUser(PagedJobQueryParams{
		UserID:   "u1",
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TotalItems != 2 || len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got len=%d total=%d", len(res.Rows), res.TotalItems)
	}

	// Verify j1 had in-memory volatile progress overlaid:
	row1 := res.Rows[0]
	if row1.ID != "j1" {
		t.Fatalf("expected row 0 to be j1, got %s", row1.ID)
	}
	if row1.ProgressPercent != 72 {
		t.Fatalf("expected overlaid progress 72, got %d", row1.ProgressPercent)
	}
	if row1.Phase != "음성 전사 중 (2단계)" {
		t.Fatalf("expected overlaid phase '음성 전사 중 (2단계)', got %q", row1.Phase)
	}
	if row1.StatusDetail != "전사 진행 중..." {
		t.Fatalf("expected overlaid status detail '전사 진행 중...', got %q", row1.StatusDetail)
	}
	if row1.SizeBytes != 500 {
		t.Fatalf("expected size 500, got %d", row1.SizeBytes)
	}

	// Verify j2 retained DB values:
	row2 := res.Rows[1]
	if row2.ID != "j2" || row2.ProgressPercent != 100 || row2.SizeBytes != 1500 {
		t.Fatalf("unexpected row2: %+v", row2)
	}
}
