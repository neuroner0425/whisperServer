package worker

import (
	"testing"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

func TestRequeuePendingQueuesCompletedTranscriptNeedingVADFallback(t *testing.T) {
	jsonStore := map[string]string{
		"transcript_json":  `{"segments":[{"from":"00:00:00,000","to":"00:00:05,000","text":"a"},{"from":"00:01:00,000","to":"00:01:05,000","text":"b"}],"source":""}`,
		"refined":          `{"paragraph":[]}`,
		"refined_timeline": "00:00:00,000 : a",
	}
	blob := service.NewJobBlobService(service.JobBlobServiceDeps{
		HasJobBlob: func(_ string, kind string) bool {
			return kind == "audio_aac"
		},
		HasJobJSON: func(_ string, kind string) bool {
			return jsonStore[kind] != ""
		},
		LoadJobJSON: func(_ string, kind string) (string, error) {
			return jsonStore[kind], nil
		},
		DeleteJobBlob: func(_ string, kind string) {},
		DeleteJobJSON: func(_ string, kind string) {
			delete(jsonStore, kind)
		},
		BlobKindAudioAAC:        "audio_aac",
		BlobKindPreview:         "preview",
		BlobKindTranscriptJSON:  "transcript_json",
		BlobKindRefinedTimeline: "refined_timeline",
		BlobKindRefined:         "refined",
	})
	fields := map[string]any{}
	worker := New(Config{
		RequeueVADFallbackOnStartup: true,
		StatusPending:               "작업 대기 중",
		StatusCompleted:             "완료",
	}, Deps{
		SetJobFields: func(_ string, got map[string]any) {
			for k, v := range got {
				fields[k] = v
			}
		},
		BlobSvc: blob,
		Logf:    func(string, ...any) {},
		Errf:    func(string, error, string, ...any) {},
	})

	worker.RequeuePending(map[string]*model.Job{
		"job-1": {
			Status:        "완료",
			StatusCode:    model.JobStatusCompletedCode,
			FileType:      "audio",
			RefineEnabled: true,
		},
	})

	if fields["status"] != "작업 대기 중" {
		t.Fatalf("expected job to be reset to pending, got fields=%v", fields)
	}
	if fields["status_code"] != model.JobStatusPendingCode {
		t.Fatalf("expected pending status code, got fields=%v", fields)
	}
	if jsonStore["refined"] != "" || jsonStore["refined_timeline"] != "" {
		t.Fatalf("expected stale refined artifacts to be deleted, got %v", jsonStore)
	}
}

func TestRequeuePendingSkipsVADFallbackWhenStartupFlagDisabled(t *testing.T) {
	jsonStore := map[string]string{
		"transcript_json": `{"segments":[{"from":"00:00:00,000","to":"00:00:05,000","text":"a"},{"from":"00:01:00,000","to":"00:01:05,000","text":"b"}]}`,
	}
	blob := service.NewJobBlobService(service.JobBlobServiceDeps{
		HasJobBlob: func(_ string, kind string) bool {
			return kind == "audio_aac"
		},
		HasJobJSON: func(_ string, kind string) bool {
			return jsonStore[kind] != ""
		},
		LoadJobJSON: func(_ string, kind string) (string, error) {
			return jsonStore[kind], nil
		},
		DeleteJobBlob:          func(_ string, kind string) {},
		DeleteJobJSON:          func(_ string, kind string) {},
		BlobKindAudioAAC:       "audio_aac",
		BlobKindTranscriptJSON: "transcript_json",
	})
	fields := map[string]any{}
	worker := New(Config{
		StatusPending:   "작업 대기 중",
		StatusCompleted: "완료",
	}, Deps{
		SetJobFields: func(_ string, got map[string]any) {
			for k, v := range got {
				fields[k] = v
			}
		},
		BlobSvc: blob,
		Logf:    func(string, ...any) {},
		Errf:    func(string, error, string, ...any) {},
	})

	worker.RequeuePending(map[string]*model.Job{
		"job-1": {
			Status:     "완료",
			StatusCode: model.JobStatusCompletedCode,
			FileType:   "audio",
		},
	})

	if len(fields) != 0 {
		t.Fatalf("expected completed job to remain unchanged without startup flag, got fields=%v", fields)
	}
}
