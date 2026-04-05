package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"whisperserver/src/internal/model"
)

func TestSanitizePreviewText(t *testing.T) {
	got := SanitizePreviewText("[00:00:01 --> 00:00:03] hello\n[meta] world\n''")
	if got != "hello\nworld" {
		t.Fatalf("unexpected preview text: %q", got)
	}
}

func TestCollectFolderSubtree(t *testing.T) {
	rt := New(Config{
		TmpFolder: t.TempDir(),
		ListAllFoldersByOwner: func(userID string, trashed bool) ([]model.Folder, error) {
			return []model.Folder{
				{ID: "root", ParentID: ""},
				{ID: "child", ParentID: "root"},
				{ID: "grand", ParentID: "child"},
			}, nil
		},
		GetFolderByID: func(userID, folderID string) (*model.Folder, error) {
			return &model.Folder{ID: folderID, ParentID: map[string]string{"root": "", "child": "root", "grand": "child"}[folderID]}, nil
		},
		SetFolderTrashed: func(userID, folderID string, trashed bool) error { return nil },
	})
	subtree := rt.CollectFolderSubtree("u1", []string{"root"}, false)
	for _, id := range []string{"root", "child", "grand"} {
		if _, ok := subtree[id]; !ok {
			t.Fatalf("missing subtree id %s", id)
		}
	}
}

func TestCleanupInactiveTempWavs(t *testing.T) {
	dir := t.TempDir()
	rt := New(Config{TmpFolder: dir})
	for _, name := range []string{"a.wav", "b.m4a", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rt.CleanupInactiveTempWavs()
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatalf("keep.txt should remain: %v", err)
	}
	for _, name := range []string{"a.wav", "b.m4a"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed", name)
		}
	}
}
