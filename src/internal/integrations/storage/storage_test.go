package storage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"whisperserver/src/internal/integrations/storage"
)

func runCommonStorageTests(t *testing.T, s storage.Storage) {
	ctx := context.Background()

	// 1. Initially object should not exist
	exists, err := s.Exists(ctx, "jobs/123/audio.m4a")
	if err != nil {
		t.Fatalf("exists error: %v", err)
	}
	if exists {
		t.Fatalf("expected object to not exist")
	}

	_, err = s.Get(ctx, "jobs/123/audio.m4a")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// 2. Put object
	data := []byte("fake-audio-binary-data")
	info, err := s.Put(ctx, "jobs/123/audio.m4a", data, "audio/mp4")
	if err != nil {
		t.Fatalf("put failed: %v", err)
	}
	if info.Size != int64(len(data)) {
		t.Fatalf("expected size %d, got %d", len(data), info.Size)
	}

	// 3. Exists & Stat
	exists, err = s.Exists(ctx, "jobs/123/audio.m4a")
	if err != nil || !exists {
		t.Fatalf("expected object to exist, got exists=%v err=%v", exists, err)
	}

	stat, err := s.Stat(ctx, "jobs/123/audio.m4a")
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if stat.Size != int64(len(data)) {
		t.Fatalf("expected size %d, got %d", len(data), stat.Size)
	}

	// 4. Get
	fetched, err := s.Get(ctx, "jobs/123/audio.m4a")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if string(fetched) != string(data) {
		t.Fatalf("got data mismatch: %s != %s", fetched, data)
	}

	// 5. Stream Get
	rc, rinfo, err := s.GetStream(ctx, "jobs/123/audio.m4a")
	if err != nil {
		t.Fatalf("get stream failed: %v", err)
	}
	defer rc.Close()
	if rinfo.Size != int64(len(data)) {
		t.Fatalf("stream info size mismatch")
	}

	// 6. Delete
	if err := s.Delete(ctx, "jobs/123/audio.m4a"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	exists, err = s.Exists(ctx, "jobs/123/audio.m4a")
	if err != nil || exists {
		t.Fatalf("expected object deleted, exists=%v", exists)
	}

	// 7. DeletePrefix
	_, _ = s.Put(ctx, "jobs/abc/file1", []byte("1"), "text/plain")
	_, _ = s.Put(ctx, "jobs/abc/file2", []byte("2"), "text/plain")
	_, _ = s.Put(ctx, "jobs/def/file3", []byte("3"), "text/plain")

	if err := s.DeletePrefix(ctx, "jobs/abc/"); err != nil {
		t.Fatalf("delete prefix failed: %v", err)
	}
	ex1, _ := s.Exists(ctx, "jobs/abc/file1")
	ex2, _ := s.Exists(ctx, "jobs/abc/file2")
	ex3, _ := s.Exists(ctx, "jobs/def/file3")
	if ex1 || ex2 {
		t.Fatalf("expected abc files deleted")
	}
	if !ex3 {
		t.Fatalf("expected def file preserved")
	}
}

func TestMemoryStorage(t *testing.T) {
	s := storage.NewMemoryStorage()
	runCommonStorageTests(t, s)
}

func TestLocalStorage(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "storage")
	defer os.RemoveAll(tmp)

	s, err := storage.NewLocalStorage(tmp)
	if err != nil {
		t.Fatalf("init local storage: %v", err)
	}
	runCommonStorageTests(t, s)
}

func TestS3StorageLocalMinIO(t *testing.T) {
	// Test against local MinIO with user-provided credentials (test / test1234!)
	cfg := storage.S3Config{
		Endpoint:        "localhost:9000",
		Region:          "us-east-1",
		Bucket:          "whisper-blobs-test",
		AccessKeyID:     "test",
		SecretAccessKey: "test1234!",
		UseSSL:          false,
	}

	s, err := storage.NewS3Storage(context.Background(), cfg)
	if err != nil {
		t.Skipf("skipping MinIO integration test: %v", err)
		return
	}

	runCommonStorageTests(t, s)
}
