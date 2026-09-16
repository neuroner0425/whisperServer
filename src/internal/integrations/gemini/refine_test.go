package gemini

import (
	"encoding/json"
	"testing"
)

func TestNormalizeRefineResponseJSONAcceptsValidJSON(t *testing.T) {
	raw := `{
  "paragraph": [
    {
      "paragraph_summary": " 요약 ",
      "sentence": [
        {"start_time": " [00:00:00,000] ", "content": " 내용 "}
      ]
    }
  ]
}`
	got, err := normalizeRefineResponseJSON(raw)
	if err != nil {
		t.Fatalf("expected valid JSON, got %v", err)
	}
	if got == "" {
		t.Fatalf("expected normalized JSON")
	}
}

func TestNormalizeRefineResponseJSONRejectsMalformedJSON(t *testing.T) {
	if _, err := normalizeRefineResponseJSON(`{"paragraph":`); err == nil {
		t.Fatalf("expected malformed JSON error")
	}
}

func TestNormalizeRefineResponseJSONRejectsEmptyStartTime(t *testing.T) {
	raw := `{
  "paragraph": [
    {
      "sentence": [
        {"start_time": "", "content": "내용"}
      ]
    }
  ]
}`
	if _, err := normalizeRefineResponseJSON(raw); err == nil {
		t.Fatalf("expected empty start_time error")
	}
}

func TestNormalizeRefineResponseJSONRejectsInvalidStartTime(t *testing.T) {
	raw := `{
  "paragraph": [
    {
      "sentence": [
        {"start_time": "not-a-time", "content": "내용"}
      ]
    }
  ]
}`
	if _, err := normalizeRefineResponseJSON(raw); err == nil {
		t.Fatalf("expected invalid start_time error")
	}
}

func TestBuildRefinedJSONFromParagraphMarkersCopiesTimelineSentences(t *testing.T) {
	timeline := "[00:00:00,000] 첫 번째 문장\n[00:00:05,000] 두 번째 문장\n[00:00:10,000] 세 번째 문장"
	sentences, err := parseTimelineSentences(timeline)
	if err != nil {
		t.Fatal(err)
	}
	markers, err := parseParagraphMarkers("[00:00:00,000] 시작\n[00:00:10,000] 다음", sentences)
	if err != nil {
		t.Fatal(err)
	}

	got, err := buildRefinedJSONFromTimeline(sentences, markers)
	if err != nil {
		t.Fatal(err)
	}

	var parsed refineResponse
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Paragraph) != 2 {
		t.Fatalf("expected 2 paragraphs, got %d", len(parsed.Paragraph))
	}
	if parsed.Paragraph[0].Sentence[1].StartTime != "[00:00:05,000]" {
		t.Fatalf("expected second sentence copied into first paragraph, got %q", parsed.Paragraph[0].Sentence[1].StartTime)
	}
	if parsed.Paragraph[1].Sentence[0].Content != "세 번째 문장" {
		t.Fatalf("expected content copied from timeline, got %q", parsed.Paragraph[1].Sentence[0].Content)
	}
}

func TestParseParagraphMarkers_SentenceNumberBased(t *testing.T) {
	timeline := "[00:00:00,000] 첫 번째 문장\n[00:00:05,000] 두 번째 문장\n[00:00:10,000] 세 번째 문장"
	sentences, err := parseTimelineSentences(timeline)
	if err != nil {
		t.Fatal(err)
	}
	markers, err := parseParagraphMarkers("[1] 첫 문단\n[3] 둘째 문단", sentences)
	if err != nil {
		t.Fatalf("parseParagraphMarkers failed: %v", err)
	}
	if len(markers) != 2 {
		t.Fatalf("expected 2 markers, got %d", len(markers))
	}
	if markers[0].StartTime != "[00:00:00,000]" || markers[1].StartTime != "[00:00:10,000]" {
		t.Fatalf("unexpected markers: %+v", markers)
	}
}

func TestParseParagraphMarkers_NearestTimestampFallback(t *testing.T) {
	timeline := "[00:00:00,000] 첫 번째 문장\n[00:15:23,500] 두 번째 문장"
	sentences, err := parseTimelineSentences(timeline)
	if err != nil {
		t.Fatal(err)
	}
	// Gemini outputs [00:15:23,580] which is 80ms off from [00:15:23,500]
	markers, err := parseParagraphMarkers("[00:15:23,580] 둘째 문단", sentences)
	if err != nil {
		t.Fatalf("expected fallback nearest match, got error: %v", err)
	}
	// Should snap to [00:15:23,500] and include first sentence
	if len(markers) != 2 || markers[1].StartTime != "[00:15:23,500]" {
		t.Fatalf("expected snapped to second sentence, got %+v", markers)
	}
}

func TestParseTimelineSentences_FlexibleTimestamps(t *testing.T) {
	raw := "[00:15,860] 시 생략 문장\n[00:3:22,270] 자릿수 축약 문장\n[00:36,140] 또 다른 시 생략 문장"
	sentences, err := parseTimelineSentences(raw)
	if err != nil {
		t.Fatalf("parseTimelineSentences failed: %v", err)
	}
	if len(sentences) != 3 {
		t.Fatalf("expected 3 sentences, got %d", len(sentences))
	}
	if sentences[0].StartTime != "[00:00:15,860]" {
		t.Errorf("expected [00:00:15,860], got %s", sentences[0].StartTime)
	}
	if sentences[1].StartTime != "[00:03:22,270]" {
		t.Errorf("expected [00:03:22,270], got %s", sentences[1].StartTime)
	}
	if sentences[2].StartTime != "[00:00:36,140]" {
		t.Errorf("expected [00:00:36,140], got %s", sentences[2].StartTime)
	}
}

func TestParseParagraphMarkers_AvoidsMisinterpretingTimestampHourAsIndex(t *testing.T) {
	timeline := "[00:00:00,000] 첫 번째 문장\n[00:30:00,000] 30분 문장\n[01:15:23,500] 1시간 15분 문장"
	sentences, err := parseTimelineSentences(timeline)
	if err != nil {
		t.Fatal(err)
	}
	// [01:15:23,580] has '01' at start. Must NOT be parsed as sentence index [1]!
	markers, err := parseParagraphMarkers("[01:15:23,580] 1시간 15분 문단", sentences)
	if err != nil {
		t.Fatalf("parseParagraphMarkers failed: %v", err)
	}
	if len(markers) != 2 {
		t.Fatalf("expected 2 markers (first sentence + 1h15m sentence), got %d", len(markers))
	}
	if markers[1].StartTime != "[01:15:23,500]" {
		t.Fatalf("expected snapped to [01:15:23,500], got %s", markers[1].StartTime)
	}
}
