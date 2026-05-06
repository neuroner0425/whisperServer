package gemini

import "testing"

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
