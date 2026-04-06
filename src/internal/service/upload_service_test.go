package service

import (
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
	"time"

	model "whisperserver/src/internal/domain"
)

func TestUploadService_Create_AudioHappyPath(t *testing.T) {
	tmp := t.TempDir()

	var (
		addedJobID     string
		addedJob       *model.Job
		savedBlobKind  string
		savedBlobBytes []byte
		enqueuedID     string
		setFieldsCalls int
	)

	s := NewUploadService(UploadServiceDeps{
		DetectFileType: func(name string) string { return "audio" },
		AllowedFile:    func(string) bool { return true },
		SortedExts:     func() []string { return []string{".mp3"} },

		ListTagNamesByOwner:     func(string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },
		GetFolderByID:           func(string, string) (*model.Folder, error) { return &model.Folder{}, nil },
		TouchFolderAndAncestors: func(string, string) error { return nil },

		SaveUploadWithLimit: func(_ *multipart.FileHeader, dst string, _ int64, _ int64) (int64, error) {
			b := []byte("src-audio")
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				return 0, err
			}
			return int64(len(b)), nil
		},
		ConvertToAac: func(_ string, dst string) error {
			return os.WriteFile(dst, []byte("aac-bytes"), 0o644)
		},
		GetMediaDuration: func(string) *int { v := 12; return &v },
		FormatSecondsPtr: func(*int) string { return "" },
		SaveJobBlob: func(_ string, kind string, b []byte) error {
			savedBlobKind = kind
			savedBlobBytes = append([]byte(nil), b...)
			return nil
		},

		BlobKindAudioAAC:    "aac",
		BlobKindPDFOriginal: "pdf",

		AddJob: func(id string, j *model.Job) {
			addedJobID = id
			addedJob = j
		},
		SetJobFields:      func(string, map[string]any) { setFieldsCalls++ },
		EnqueueTranscribe: func(id string) { enqueuedID = id },

		TmpFolder:           tmp,
		MaxUploadSizeMB:     10,
		UploadRateLimitKBPS: 1000,
		StatusPending:       "pending",
		StatusFailed:        "failed",

		Now:      func() time.Time { return time.Unix(0, 0) },
		NewJobID: func() string { return "job-1" },
		Spawn:    func(fn func()) { fn() },
	})

	fh := &multipart.FileHeader{
		Filename: "a.mp3",
		Header:   textproto.MIMEHeader{"Content-Type": []string{"audio/mpeg"}},
	}
	jobID, filename, err := s.Create(UploadCreateRequest{
		OwnerID:     "u1",
		DisplayName: "hello",
		FileHeader:  fh,
	})
	if err != nil {
		t.Fatalf("Create err=%v", err)
	}
	if jobID != "job-1" {
		t.Fatalf("jobID=%q", jobID)
	}
	if filename != "hello.mp3" {
		t.Fatalf("filename=%q", filename)
	}
	if addedJobID != "job-1" || addedJob == nil {
		t.Fatalf("AddJob not called")
	}
	if addedJob.FileType != "audio" {
		t.Fatalf("addedJob.FileType=%q", addedJob.FileType)
	}
	if savedBlobKind != "aac" || string(savedBlobBytes) != "aac-bytes" {
		t.Fatalf("saved blob kind=%q bytes=%q", savedBlobKind, string(savedBlobBytes))
	}
	if enqueuedID != "job-1" {
		t.Fatalf("enqueuedID=%q", enqueuedID)
	}
	if setFieldsCalls == 0 {
		t.Fatalf("expected SetJobFields to be called")
	}

	if _, err := os.Stat(filepath.Join(tmp, "job-1_temp.mp3")); !os.IsNotExist(err) {
		t.Fatalf("temp upload file should be removed; stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "job-1.m4a")); !os.IsNotExist(err) {
		t.Fatalf("aac temp file should be removed; stat err=%v", err)
	}
}

func TestUploadService_Create_PDFHappyPath(t *testing.T) {
	tmp := t.TempDir()

	var (
		savedBlobKind string
		enqueuedID    string
	)

	s := NewUploadService(UploadServiceDeps{
		DetectFileType: func(name string) string { return "pdf" },
		AllowedFile:    func(string) bool { return true },
		SortedExts:     func() []string { return []string{".pdf"} },

		ListTagNamesByOwner: func(string) (map[string]struct{}, error) { return map[string]struct{}{}, nil },

		SaveUploadWithLimit: func(_ *multipart.FileHeader, dst string, _ int64, _ int64) (int64, error) {
			b := []byte("%PDF")
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				return 0, err
			}
			return int64(len(b)), nil
		},
		FormatSecondsPtr: func(*int) string { return "" },
		SaveJobBlob: func(_ string, kind string, _ []byte) error {
			savedBlobKind = kind
			return nil
		},

		BlobKindAudioAAC:    "aac",
		BlobKindPDFOriginal: "pdf",

		AddJob:            func(string, *model.Job) {},
		SetJobFields:      func(string, map[string]any) {},
		EnqueuePDFExtract: func(id string) { enqueuedID = id },

		TmpFolder:           tmp,
		MaxUploadSizeMB:     10,
		UploadRateLimitKBPS: 1000,
		StatusPending:       "pending",
		StatusFailed:        "failed",

		NewJobID: func() string { return "job-pdf" },
		Spawn:    func(fn func()) { fn() },
	})

	fh := &multipart.FileHeader{
		Filename: "x.pdf",
		Header:   textproto.MIMEHeader{"Content-Type": []string{"application/pdf"}},
	}
	jobID, filename, err := s.Create(UploadCreateRequest{
		OwnerID:    "u1",
		FileHeader: fh,
	})
	if err != nil {
		t.Fatalf("Create err=%v", err)
	}
	if jobID != "job-pdf" {
		t.Fatalf("jobID=%q", jobID)
	}
	if filename != "x.pdf" {
		t.Fatalf("filename=%q", filename)
	}
	if savedBlobKind != "pdf" {
		t.Fatalf("savedBlobKind=%q", savedBlobKind)
	}
	if enqueuedID != "job-pdf" {
		t.Fatalf("enqueuedID=%q", enqueuedID)
	}
}
