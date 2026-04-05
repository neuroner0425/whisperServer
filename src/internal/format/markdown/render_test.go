package markdown

import (
	"strings"
	"testing"
)

func TestRenderMarkdownText(t *testing.T) {
	html := string(RenderMarkdownText("# Title\n\n- A\n- B\n\n| H1 | H2 |\n| --- | --- |\n| 1 | 2 |"))
	for _, want := range []string{"<h1>Title</h1>", "<ul>", "<li>A</li>", `<table class="document-table">`} {
		if !strings.Contains(html, want) {
			t.Fatalf("html missing %q: %s", want, html)
		}
	}
}
