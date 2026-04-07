package service

import "testing"

func TestRenderTranscriptMarkdown(t *testing.T) {
	raw := `{"segments":[{"from":"00:00:00,000","to":"00:00:01,000","text":" 안녕하세요 "},{"from":"00:00:01,000","to":"00:00:02,000","text":" 반갑습니다 "},{"from":"00:00:02,000","to":"00:00:03,000","text":"  "}]}`

	got, err := RenderTranscriptMarkdown(raw)
	if err != nil {
		t.Fatalf("RenderTranscriptMarkdown() error = %v", err)
	}

	want := "안녕하세요 반갑습니다"
	if got != want {
		t.Fatalf("RenderTranscriptMarkdown() = %q, want %q", got, want)
	}
}

func TestRenderRefinedMarkdown(t *testing.T) {
	raw := `{"paragraph":[{"paragraph_summary":"첫 번째 요약","sentence":[{"start_time":"[00:00:00,000]","content":" 안녕하세요 "},{"start_time":"[00:00:01,000]","content":" 반갑습니다 "} ]},{"paragraph_summary":"두 번째 요약","sentence":[{"start_time":"[00:00:02,000]","content":" 다음 문장입니다 "}]}]}`

	got, err := RenderRefinedMarkdown(raw)
	if err != nil {
		t.Fatalf("RenderRefinedMarkdown() error = %v", err)
	}

	want := "## 첫 번째 요약\n\n안녕하세요 반갑습니다\n\n## 두 번째 요약\n\n다음 문장입니다"
	if got != want {
		t.Fatalf("RenderRefinedMarkdown() = %q, want %q", got, want)
	}
}

func TestRenderDownloadMarkdownTitle(t *testing.T) {
	got := RenderDownloadMarkdownTitle("sample-file", "본문입니다.")
	want := "# sample-file\n\n본문입니다."
	if got != want {
		t.Fatalf("RenderDownloadMarkdownTitle() = %q, want %q", got, want)
	}
}
