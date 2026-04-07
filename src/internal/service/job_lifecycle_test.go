package service

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestJobLifecycle_ResetForTranscribe(t *testing.T) {
	deleted := []string{}
	var setFields map[string]any
	s := NewJobLifecycle(JobLifecycleDeps{
		Now:           func() time.Time { return time.Unix(123, 0) },
		CancelJob:     func(string) {},
		RemoveTempWav: func(string) {},
		SetJobFields:  func(_ string, fields map[string]any) { setFields = fields },
		DeleteJobBlob: func(_ string, kind string) { deleted = append(deleted, kind) },
		DeleteJobJSON: func(_ string, kind string) { deleted = append(deleted, kind) },
		StatusPending: "작업 대기 중",
	})

	s.ResetForTranscribe("job1", true)

	if setFields == nil {
		t.Fatalf("expected SetJobFields to be called")
	}
	if got := setFields["status"]; got != "작업 대기 중" {
		t.Fatalf("status mismatch: %v", got)
	}
	if got := setFields["refine_enabled"]; got != true {
		t.Fatalf("refine_enabled mismatch: %v", got)
	}

	sort.Strings(deleted)
	want := []string{"preview", "refined", "transcript_json"}
	sort.Strings(want)
	if !reflect.DeepEqual(deleted, want) {
		t.Fatalf("deleted kinds mismatch:\n got=%v\nwant=%v", deleted, want)
	}
}

func TestJobLifecycle_ClearPDFProcessingBlobs(t *testing.T) {
	deleted := []string{}
	s := NewJobLifecycle(JobLifecycleDeps{
		DeleteJobBlob: func(_ string, kind string) { deleted = append(deleted, kind) },
		DeleteJobJSON: func(_ string, kind string) { deleted = append(deleted, kind) },
		ListJobBlobKinds: func(string) ([]string, error) {
			return []string{
				"document_chunk_1",
				"document_chunk_2",
				"audio_aac",
			}, nil
		},
	})

	s.ClearPDFProcessingBlobs("job1")
	sort.Strings(deleted)

	// Must include fixed kinds plus chunk kinds.
	wantContains := []string{"document_chunk_1", "document_chunk_2", "document_chunk_index", "document_json", "preview"}
	for _, w := range wantContains {
		found := false
		for _, d := range deleted {
			if d == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected deleted to contain %q, got=%v", w, deleted)
		}
	}
}

func TestJobLifecycle_MarkTrashed(t *testing.T) {
	var setFields map[string]any
	s := NewJobLifecycle(JobLifecycleDeps{
		Now:          func() time.Time { return time.Unix(0, 0).UTC() },
		SetJobFields: func(_ string, fields map[string]any) { setFields = fields },
	})
	s.MarkTrashed("job1")
	if setFields == nil {
		t.Fatalf("expected SetJobFields to be called")
	}
	if got := setFields["is_trashed"]; got != true {
		t.Fatalf("is_trashed mismatch: %v", got)
	}
	if got := setFields["deleted_at"]; got != "1970-01-01 00:00:00" {
		t.Fatalf("deleted_at mismatch: %v", got)
	}
}
