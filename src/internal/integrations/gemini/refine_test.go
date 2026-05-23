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

func TestParseParagraphMarkersRejectsUnknownTimestamp(t *testing.T) {
	sentences, err := parseTimelineSentences("[00:00:00,000] 첫 번째 문장")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseParagraphMarkers("[00:00:01,000] 없는 시작", sentences); err == nil {
		t.Fatalf("expected unknown timestamp error")
	}
}
