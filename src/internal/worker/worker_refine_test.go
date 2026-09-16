package worker

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/service"
)

func TestValidateRefinedCoverageAcceptsExactTimestampCoverage(t *testing.T) {
	timeline := "[00:00:00,000] 첫 번째 문장\n[00:00:05,000] 두 번째 문장\n[00:00:10,000] 세 번째 문장"
	refined := `{
  "paragraph": [
    {
      "sentence": [
        {"start_time": "[00:00:00,000]", "content": "첫 번째 문장"},
        {"start_time": "[00:00:05,000]", "content": "두 번째 문장"},
        {"start_time": "[00:00:10,000]", "content": "세 번째 문장"}
      ]
    }
  ]
}`
	if err := validateRefinedCoverage(timeline, refined); err != nil {
		t.Fatalf("expected valid refined coverage, got %v", err)
	}
}

func TestValidateRefinedCoverageRejectsMissingTimestamp(t *testing.T) {
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
		t.Fatalf("expected missing timestamp error")
	}
}

func TestValidateTimelineCoverageRejectsMissingTimestamp(t *testing.T) {
	original := "[00:00:00,000] 1\n[00:00:05,000] 2\n[00:00:10,000] 3"
	polished := "[00:00:00,000] 1\n[00:00:10,000] 3"
	if err := validateTimelineCoverage(original, polished); err == nil {
		t.Fatalf("expected missing timestamp error")
	}
}

func TestValidateTimelineCoverageAllowsUpToThreePercentMissing(t *testing.T) {
	original := make([]string, 0, 100)
	polished := make([]string, 0, 97)
	for i := 0; i < 100; i++ {
		line := fmt.Sprintf("[00:%02d:%02d,000] line", i/60, i%60)
		original = append(original, line)
		if i < 97 {
			polished = append(polished, line)
		}
	}
	if err := validateTimelineCoverage(strings.Join(original, "\n"), strings.Join(polished, "\n")); err != nil {
		t.Fatalf("expected 3 percent missing timestamps to pass, got %v", err)
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

	if err := w.taskRefining("job1", "[00:00:00,000] 원문"); err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	if store["refined_timeline"] == "" {
		t.Fatalf("expected refined_timeline to be preserved")
	}
	if store["refined"] == "" {
		t.Fatalf("expected fallback refined result to be saved")
	}
}

func TestTaskRefiningRepairsChangedGeneratedTimestamps(t *testing.T) {
	store := map[string]string{}
	var structuredInput string
	w := newRefineTestWorker(store, func(string, string) (string, error) {
		return "[00:00:00,050] 다듬은 첫 문장\n[00:00:05,050] 다듬은 둘째 문장", nil
	}, func(timeline, _ string) (string, error) {
		structuredInput = timeline
		return `{"paragraph":[{"sentence":[{"start_time":"[00:00:00,000]","content":"다듬은 첫 문장"},{"start_time":"[00:00:05,000]","content":"다듬은 둘째 문장"}]}]}`, nil
	})

	err := w.taskRefining("job1", "[00:00:00,000] 원문 1\n[00:00:05,000] 원문 2")
	if err != nil {
		t.Fatalf("expected repaired timestamp result to succeed, got %v", err)
	}
	want := "[00:00:00,000] 다듬은 첫 문장\n[00:00:05,000] 다듬은 둘째 문장"
	if structuredInput != want || store["refined_timeline"] != want {
		t.Fatalf("expected original timestamps restored, structured=%q saved=%q", structuredInput, store["refined_timeline"])
	}
	if store["refine_polished_timeline_raw_candidate"] == "" {
		t.Fatalf("expected raw timestamp-changing response to be recorded")
	}
}

func TestRepairTimelineTimestampsAllowsOneSecondDriftWhenLinesAreMissing(t *testing.T) {
	original := make([]string, 0, 100)
	polished := make([]string, 0, 99)
	for i := 0; i < 100; i++ {
		second := i * 3
		line := fmt.Sprintf("[00:%02d:%02d,000] line %d", second/60, second%60, i)
		original = append(original, line)
		if i != 50 {
			polished = append(polished, fmt.Sprintf("[00:%02d:%02d,500] refined %d", second/60, second%60, i))
		}
	}

	repaired, changed, err := repairTimelineTimestamps(strings.Join(original, "\n"), strings.Join(polished, "\n"))
	if err != nil || !changed {
		t.Fatalf("expected timestamp drift to be repaired, changed=%v err=%v", changed, err)
	}
	if err := validateTimelineCoverage(strings.Join(original, "\n"), repaired); err != nil {
		t.Fatalf("expected repaired output with one omitted line to pass, got %v", err)
	}
	if strings.Contains(repaired, ",500]") {
		t.Fatalf("expected all matched timestamps to be restored to source values")
	}
}

func TestRepairTimelineTimestampsRejectsAmbiguousOneSecondMatch(t *testing.T) {
	original := "[00:00:00,000] a\n[00:00:00,800] b\n[00:00:05,000] c"
	polished := "[00:00:00,400] combined\n[00:00:05,000] c"

	_, _, err := repairTimelineTimestamps(original, polished)
	if err == nil {
		t.Fatalf("expected timestamp close to multiple source sentences to be rejected")
	}
}

func TestTaskRefiningDiscardsInvalidCachedTimelineAndRegenerates(t *testing.T) {
	store := map[string]string{"refined_timeline": "[00:00:00,000] 오래된 문장\n[00:00:01,000] 추가 문장"}
	var polishCalls int
	w := newRefineTestWorker(store, func(string, string) (string, error) {
		polishCalls++
		return "[00:00:00,000] 새 문장", nil
	}, func(string, _ string) (string, error) {
		return `{"paragraph":[{"sentence":[{"start_time":"[00:00:00,000]","content":"새 문장"}]}]}`, nil
	})

	if err := w.taskRefining("job1", "[00:00:00,000] 원문"); err != nil {
		t.Fatalf("expected invalid cache to be regenerated, got %v", err)
	}
	if polishCalls != 1 {
		t.Fatalf("expected a fresh polish call, got %d", polishCalls)
	}
	if store["refine_invalid_cached_timeline"] == "" || store["refined_timeline"] != "[00:00:00,000] 새 문장" {
		t.Fatalf("expected invalid cache recorded and replaced, store=%v", store)
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

	if err := w.taskRefining("job1", "[00:00:00,000] 원문"); err != nil {
		t.Fatalf("expected fallback to raw transcript to succeed, got %v", err)
	}
	if store["refined_timeline"] != "" {
		t.Fatalf("expected refined_timeline not to be saved")
	}
	if structureCalls != 0 {
		t.Fatalf("expected structure step to be skipped, got %d calls", structureCalls)
	}
}

func TestTaskRefiningIncludesPreviousDiagnosticsInRetryDescription(t *testing.T) {
	store := map[string]string{
		"refined_timeline":   "[00:00:00,000] 다듬은 문장",
		"refine_diagnostics": `{"step":"coverage_failure","missing_from_output":["00:00:01,000"]}`,
	}
	var description string
	w := newRefineTestWorker(store, func(string, string) (string, error) {
		return "", nil
	}, func(_ string, desc string) (string, error) {
		description = desc
		return `{"paragraph":[{"sentence":[{"start_time":"[00:00:00,000]","content":"다듬은 문장"}]}]}`, nil
	})

	if err := w.taskRefining("job1", "[00:00:00,000] 원문"); err != nil {
		t.Fatalf("expected retry success, got %v", err)
	}
	if !strings.Contains(description, "Previous Refinement Failure Feedback") || !strings.Contains(description, "coverage_failure") {
		t.Fatalf("expected previous diagnostics in refine description, got %q", description)
	}
}

func newRefineTestWorker(
	jsonStore map[string]string,
	polish func(string, string) (string, error),
	structure func(string, string) (string, error),
) *Worker {
	return newRefineTestWorkerWithFields(jsonStore, nil, polish, structure)
}

func newRefineTestWorkerWithFields(
	jsonStore map[string]string,
	fieldsStore map[string]any,
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
		DeleteJobJSON: func(_ string, kind string) {
			delete(jsonStore, kind)
		},
		BlobKindRefinedTimeline: "refined_timeline",
		BlobKindRefined:         "refined",
	})
	return &Worker{
		cfg: Config{
			StatusRefining:  "정제 중",
			StatusCompleted: "완료",
		},
		deps: Deps{
			GetJob: func(string) *model.Job {
				return &model.Job{FileType: "audio"}
			},
			SetJobFields: func(_ string, fields map[string]any) {
				if fieldsStore != nil {
					for k, v := range fields {
						fieldsStore[k] = v
					}
				}
			},
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

func TestTaskRefiningFallbackToPolishedTimelineOnStructureError(t *testing.T) {
	store := map[string]string{}
	fields := map[string]any{}
	w := newRefineTestWorkerWithFields(store, fields, func(string, string) (string, error) {
		return "[00:15,860] 다듬어진 첫 문장\n[00:00:20,000] 다듬어진 두 번째 문장", nil
	}, func(string, string) (string, error) {
		return "", errors.New("gemini structure timeout")
	})

	err := w.taskRefining("job1", "[00:00:15,860] 원본 1\n[00:00:20,000] 원본 2")
	if err != nil {
		t.Fatalf("expected taskRefining to succeed via fallback, got %v", err)
	}
	if fields["status"] != "완료" {
		t.Fatalf("expected status to be 완료, got %v", fields["status"])
	}
	if fields["result_refined"] != "db://refined" {
		t.Fatalf("expected result_refined to be db://refined, got %v", fields["result_refined"])
	}
	if !strings.Contains(fmt.Sprint(fields["status_detail"]), "다듬어진 전사본으로 완료") {
		t.Fatalf("unexpected status_detail: %v", fields["status_detail"])
	}
	if store["refined"] == "" {
		t.Fatalf("expected refined blob to be saved")
	}
	if !strings.Contains(store["refined"], "[00:00:15,860]") {
		t.Fatalf("expected normalized timestamp [00:00:15,860] in refined JSON, got: %s", store["refined"])
	}
}

func TestTaskRefiningFallbackToRawTranscriptWhenPolishedTimelineInvalid(t *testing.T) {
	store := map[string]string{}
	fields := map[string]any{}
	w := newRefineTestWorkerWithFields(store, fields, func(string, string) (string, error) {
		return "완전 깨진 텍스트 (타임스탬프 전혀 없음)", nil
	}, func(string, string) (string, error) {
		return "", errors.New("structure failed")
	})

	err := w.taskRefining("job1", "[00:00:15,860] 원본 1")
	if err != nil {
		t.Fatalf("expected fallback to raw transcript to succeed, got %v", err)
	}
	if fields["status"] != "완료" {
		t.Fatalf("expected status to be 완료, got %v", fields["status"])
	}
	if fields["result"] != "db://transcript_json" {
		t.Fatalf("expected result to be db://transcript_json, got %v", fields["result"])
	}
	if !strings.Contains(fmt.Sprint(fields["status_detail"]), "원본 전사본으로 완료") {
		t.Fatalf("unexpected status_detail: %v", fields["status_detail"])
	}
}

func TestTaskRefiningFallbackToRawTranscriptOnPolishError(t *testing.T) {
	store := map[string]string{}
	fields := map[string]any{}
	w := newRefineTestWorkerWithFields(store, fields, func(string, string) (string, error) {
		return "", errors.New("polish gemini error")
	}, func(string, string) (string, error) {
		return "", nil
	})

	err := w.taskRefining("job1", "[00:00:15,860] 원본 1")
	if err != nil {
		t.Fatalf("expected fallback to raw transcript to succeed, got %v", err)
	}
	if fields["status"] != "완료" {
		t.Fatalf("expected status to be 완료, got %v", fields["status"])
	}
	if fields["result"] != "db://transcript_json" {
		t.Fatalf("expected result to be db://transcript_json, got %v", fields["result"])
	}
	if !strings.Contains(fmt.Sprint(fields["status_detail"]), "AI 문장 다듬기에 실패하여 원본 전사본으로 완료") {
		t.Fatalf("unexpected status_detail: %v", fields["status_detail"])
	}
}

