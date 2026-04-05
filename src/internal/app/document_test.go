package app

import (
	"strings"
	"testing"
)

func TestMergeDocumentJSON(t *testing.T) {
	first := []byte(`{"pages":[{"page_index":1,"elements":[{"text":"A"}]}]}`)
	second := []byte(`{"pages":[{"page_index":2,"elements":[{"text":"B"}]}]}`)

	merged, err := mergeDocumentJSON(first, second)
	if err != nil {
		t.Fatalf("mergeDocumentJSON returned error: %v", err)
	}
	got := string(merged)
	if !strings.Contains(got, `"page_index": 1`) || !strings.Contains(got, `"page_index": 2`) {
		t.Fatalf("merged JSON missing page indexes: %s", got)
	}
}

func TestRenderDocumentMarkdown(t *testing.T) {
	raw := []byte(`{
	  "pages": [
	    {
	      "page_index": 1,
	      "elements": [
	        { "header": { "level": 1, "text": "Doc" } },
	        { "text": "Paragraph" },
	        { "list": ["One", "Two"] },
	        { "table": { "title": "Grid", "rows": [ { "cells": ["A", "B"] }, { "cells": ["1", "2"] } ] } }
	      ]
	    }
	  ]
	}`)

	out, err := renderDocumentMarkdown(raw)
	if err != nil {
		t.Fatalf("renderDocumentMarkdown returned error: %v", err)
	}
	for _, want := range []string{"## Page 1", "# Doc", "Paragraph", "- One", "| A | B |"} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown missing %q: %s", want, out)
		}
	}
}

func TestRenderDocumentMarkdownFormatsMath(t *testing.T) {
	raw := []byte(`{
	  "pages": [
	    {
	      "page_index": 1,
	      "elements": [
	        { "text": "행렬 A=(a_ij)에서 a_ij가 다음과 같을 때, 행렬 A를 구하라." },
	        { "text": "\\begin{pmatrix} x & y \\\\ 2 & 1 \\end{pmatrix} \\begin{pmatrix} x & 0 \\\\ y & x \\end{pmatrix} = 2 \\begin{pmatrix} 1 & 0 \\\\ 3 & 0 \\end{pmatrix}" }
	      ]
	    }
	  ]
	}`)

	out, err := renderDocumentMarkdown(raw)
	if err != nil {
		t.Fatalf("renderDocumentMarkdown returned error: %v", err)
	}
	for _, want := range []string{
		"행렬 $A=(a_ij)$에서 $a_ij$가 다음과 같을 때",
		"$$\n\\begin{pmatrix} x & y \\\\ 2 & 1 \\end{pmatrix} \\begin{pmatrix} x & 0 \\\\ y & x \\end{pmatrix} = 2 \\begin{pmatrix} 1 & 0 \\\\ 3 & 0 \\end{pmatrix}\n$$",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown missing %q: %s", want, out)
		}
	}
}

func TestRenderMarkdownText(t *testing.T) {
	html := string(renderMarkdownText("# Title\n\n- A\n- B\n\n| H1 | H2 |\n| --- | --- |\n| 1 | 2 |"))
	for _, want := range []string{"<h1>Title</h1>", "<ul>", "<li>A</li>", `<table class="document-table">`} {
		if !strings.Contains(html, want) {
			t.Fatalf("html missing %q: %s", want, html)
		}
	}
}

func TestTruncateConsistencyContext(t *testing.T) {
	got := truncateConsistencyContext([]string{
		"Document title: Sample",
		"Observed headers: One | Two | Three",
		"Important titles/terms: Alpha | Beta | Gamma",
	}, 45)
	if !strings.Contains(got, "Document title: Sample") {
		t.Fatalf("title should be preserved first: %s", got)
	}
	if len(got) > 45 {
		t.Fatalf("context length exceeded: %d", len(got))
	}
}
