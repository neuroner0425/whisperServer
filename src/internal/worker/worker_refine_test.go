package worker

import (
	"errors"
	"testing"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

func TestValidateRefinedCoverageAcceptsEnoughSentences(t *testing.T) {
	timeline := "[00:00:00,000] 첫 번째 문장\n[00:00:05,000] 두 번째 문장\n[00:00:10,000] 세 번째 문장"
	refined := `{
  "paragraph": [
    {
      "sentence": [
        {"start_time": "[00:00:00,000]", "content": "첫 번째 문장"},
        {"start_time": "[00:00:05,000]", "content": "두 번째 문장"}
      ]
    }
  ]
}`
	if err := validateRefinedCoverage(timeline, refined); err != nil {
		t.Fatalf("expected valid refined coverage, got %v", err)
	}
}

func TestValidateRefinedCoverageRejectsLowSentenceCoverage(t *testing.T) {
	timeline := "[00:00:00,000] 1\n[00:00:05,000] 2\n[00:00:10,000] 3\n[00:00:15,000] 4\n[00:00:20,000] 5"
	refined := `{
  "paragraph": [
    {
      "sentence": [
        {"start_time": "[00:00:00,000]", "content": "1"},
        {"start_time": "[00:00:05,000]", "content": "2"}
      ]
    }
  ]
}`
	if err := validateRefinedCoverage(timeline, refined); err == nil {
		t.Fatalf("expected low coverage error")
	}
}

func TestValidateRefinedCoverageRejectsInvalidStartTime(t *testing.T) {
	timeline := "[00:00:00,000] 첫 번째 문장"
	refined := `{
  "paragraph": [
    {
      "sentence": [
        {"start_time": "", "content": "첫 번째 문장"}
      ]
    }
  ]
}`
	if err := validateRefinedCoverage(timeline, refined); err == nil {
		t.Fatalf("expected invalid timestamp error")
	}
}

func TestTaskRefiningReusesSavedRefinedTimeline(t *testing.T) {
	store := map[string]string{"refined_timeline": "[00:00:00,000] 다듬은 문장"}
	var polishCalls int
	var structuredInput string
	w := newRefineTestWorker(store, func(string, string) (string, error) {
		polishCalls++
		return "", nil
	}, func(timeline, _ string) (string, error) {
		structuredInput = timeline
		return `{"paragraph":[{"sentence":[{"start_time":"[00:00:00,000]","content":"다듬은 문장"}]}]}`, nil
	})

	if err := w.taskRefining("job1", "[00:00:00,000] 원문"); err != nil {
		t.Fatalf("expected refine success, got %v", err)
	}
	if polishCalls != 0 {
		t.Fatalf("expected polish step to be skipped, got %d calls", polishCalls)
	}
	if structuredInput != store["refined_timeline"] {
		t.Fatalf("expected saved refined_timeline to be reused, got %q", structuredInput)
	}
	if store["refined"] == "" {
		t.Fatalf("expected final refined result to be saved")
	}
}

func TestTaskRefiningPreservesRefinedTimelineOnStructureFailure(t *testing.T) {
	store := map[string]string{}
	w := newRefineTestWorker(store, func(string, string) (string, error) {
		return "[00:00:00,000] 다듬은 문장", nil
	}, func(string, string) (string, error) {
		return "", errors.New("structure failed")
	})

	if err := w.taskRefining("job1", "[00:00:00,000] 원문"); err == nil {
		t.Fatalf("expected structure failure")
	}
	if store["refined_timeline"] == "" {
		t.Fatalf("expected refined_timeline to be preserved")
	}
	if store["refined"] != "" {
		t.Fatalf("expected final refined result not to be saved")
	}
}

func TestTaskRefiningDoesNotSaveRefinedTimelineOnPolishFailure(t *testing.T) {
	store := map[string]string{}
	var structureCalls int
	w := newRefineTestWorker(store, func(string, string) (string, error) {
		return "", errors.New("polish failed")
	}, func(string, string) (string, error) {
		structureCalls++
		return "", nil
	})

	if err := w.taskRefining("job1", "[00:00:00,000] 원문"); err == nil {
		t.Fatalf("expected polish failure")
	}
	if store["refined_timeline"] != "" {
		t.Fatalf("expected refined_timeline not to be saved")
	}
	if structureCalls != 0 {
		t.Fatalf("expected structure step to be skipped, got %d calls", structureCalls)
	}
}

func newRefineTestWorker(
	jsonStore map[string]string,
	polish func(string, string) (string, error),
	structure func(string, string) (string, error),
) *Worker {
	blob := service.NewJobBlobService(service.JobBlobServiceDeps{
		HasJobJSON: func(_ string, kind string) bool {
			return jsonStore[kind] != ""
		},
		LoadJobJSON: func(_ string, kind string) (string, error) {
			return jsonStore[kind], nil
		},
		SaveJobJSON: func(_ string, kind string, data string) error {
			jsonStore[kind] = data
			return nil
		},
		BlobKindRefinedTimeline: "refined_timeline",
		BlobKindRefined:         "refined",
	})
	return &Worker{
		cfg: Config{
			StatusRefining: "정제 중",
		},
		deps: Deps{
			GetJob: func(string) *model.Job {
				return &model.Job{FileType: "audio"}
			},
			SetJobFields:                  func(string, map[string]any) {},
			BlobSvc:                       blob,
			HasGeminiConfigured:           func() bool { return true },
			PolishTranscriptTimeline:      polish,
			StructureTranscriptParagraphs: structure,
			UniqueStrings:                 func(tags []string) []string { return tags },
			Logf:                          func(string, ...any) {},
			Errf:                          func(string, error, string, ...any) {},
		},
	}
}
