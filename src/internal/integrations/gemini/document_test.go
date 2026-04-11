package gemini

import (
	"strings"
	"testing"

	"whisperserver/src/internal/structured"
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
	        { "list": { "items": [ { "text": "One" }, { "text": "Two" } ] } },
	        { "code": { "languages": "python", "raw": "print('hi')" } },
	        { "table": { "title": "Grid", "rows": [ { "cells": ["A", "B"] }, { "cells": ["1", "2"] } ] } }
	      ]
	    }
	  ]
	}`)

	out, err := renderDocumentMarkdown(raw)
	if err != nil {
		t.Fatalf("renderDocumentMarkdown returned error: %v", err)
	}
	for _, want := range []string{"## Page 1", "# Doc", "Paragraph", "- One", "```python", "print('hi')", "| A | B |"} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown missing %q: %s", want, out)
		}
	}
}

func TestRenderDocumentMarkdownNestedList(t *testing.T) {
	raw := []byte(`{
	  "pages": [
	    {
	      "page_index": 1,
	      "elements": [
	        {
	          "list": {
	            "items": [
	              {
	                "text": "가 항목 내용",
	                "children": [
	                  { "text": "가" },
	                  { "text": "나" }
	                ]
	              },
	              {
	                "text": "나 항목 내용",
	                "children": [
	                  { "text": "다" },
	                  { "text": "라" }
	                ]
	              }
	            ]
	          }
	        }
	      ]
	    }
	  ]
	}`)

	out, err := renderDocumentMarkdown(raw)
	if err != nil {
		t.Fatalf("renderDocumentMarkdown returned error: %v", err)
	}
	for _, want := range []string{
		"- 가 항목 내용",
		"  - 가",
		"  - 나",
		"- 나 항목 내용",
		"  - 다",
		"  - 라",
	} {
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
	        { "text": "행렬 A에서 다음 식을 보자." },
	        { "math_inline": "A=(a_ij)" },
	        { "math_block": "\\begin{pmatrix} x & y \\\\ 2 & 1 \\end{pmatrix} \\begin{pmatrix} x & 0 \\\\ y & x \\end{pmatrix} = 2 \\begin{pmatrix} 1 & 0 \\\\ 3 & 0 \\end{pmatrix}" }
	      ]
	    }
	  ]
	}`)

	out, err := renderDocumentMarkdown(raw)
	if err != nil {
		t.Fatalf("renderDocumentMarkdown returned error: %v", err)
	}
	for _, want := range []string{
		"행렬 A에서 다음 식을 보자.",
		"$A=(a_ij)$",
		"$$\n\\begin{pmatrix} x & y \\\\ 2 & 1 \\end{pmatrix} \\begin{pmatrix} x & 0 \\\\ y & x \\end{pmatrix} = 2 \\begin{pmatrix} 1 & 0 \\\\ 3 & 0 \\end{pmatrix}\n$$",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown missing %q: %s", want, out)
		}
	}
}

func TestNormalizeDocumentResponseJSONTrimsMathFields(t *testing.T) {
	raw := `{
	  "pages": [
	    {
	      "page_index": 1,
	      "elements": [
	        { "math_inline": "  x+y  " },
	        { "math_block": "  \\begin{pmatrix}1\\end{pmatrix}  " },
	        { "code": { "languages": "  ", "raw": "  print(1)\n  " } }
	      ]
	    }
	  ]
	}`

	out, err := normalizeDocumentResponseJSON(raw)
	if err != nil {
		t.Fatalf("normalizeDocumentResponseJSON returned error: %v", err)
	}
	got := string(out)
	for _, want := range []string{`"math_inline": "x+y"`, `"math_block": "\\begin{pmatrix}1\\end{pmatrix}"`, `"languages": "text"`, `"raw": "print(1)"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized JSON missing %q: %s", want, got)
		}
	}
}

func TestNormalizeDocumentResponseJSONAddsEnglishImagePrefixes(t *testing.T) {
	raw := `{
	  "pages": [
	    {
	      "page_index": 1,
	      "elements": [
	        { "header": { "level": 1, "text": "선형대수 공지" } },
	        { "text": "강의 소개와 동아리 예시를 설명한다." },
	        { "img": { "title": "Photo: University club recruitment posters", "description": "Description: A photo showing various recruitment posters on a wall." } },
	        { "table": { "title": "수강 계획", "rows": [ { "cells": ["주차", "내용"] }, { "cells": ["1", "소개"] } ] } }
	      ]
	    },
	    {
	      "page_index": 2,
	      "elements": [
	        { "header": { "level": 1, "text": "Linear Algebra Notice" } },
	        { "text": "This page introduces the lecture materials." },
	        { "img": { "title": "사진: 교재 표지", "description": "설명: 강의 교재 표지 이미지." } },
	        { "table": { "title": "Table: Schedule", "rows": [ { "cells": ["Week", "Topic"] }, { "cells": ["1", "Intro"] } ] } }
	      ]
	    }
	  ]
	}`

	out, err := normalizeDocumentResponseJSON(raw)
	if err != nil {
		t.Fatalf("normalizeDocumentResponseJSON returned error: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		`"title": "Photo: University club recruitment posters"`,
		`"description": "Description: A photo showing various recruitment posters on a wall."`,
		`"title": "Table: 수강 계획"`,
		`"title": "Photo: 사진: 교재 표지"`,
		`"description": "Description: 설명: 강의 교재 표지 이미지."`,
		`"title": "Table: Schedule"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized JSON missing %q: %s", want, got)
		}
	}
}

func TestTruncateConsistencyContext(t *testing.T) {
	got, err := structured.BuildConsistencyContext([]byte(`{
	  "pages": [
	    {
	      "page_index": 1,
	      "elements": [
	        { "header": { "level": 1, "text": "Sample" } },
	        { "header": { "level": 2, "text": "One" } },
	        { "header": { "level": 2, "text": "Two" } },
	        { "header": { "level": 2, "text": "Three" } },
	        { "img": { "title": "Alpha", "description": "Description: Alpha" } },
	        { "table": { "title": "Beta", "rows": [ { "cells": ["A"] } ] } },
	        { "img": { "title": "Gamma", "description": "Description: Gamma" } }
	      ]
	    }
	  ]
	}`), 45)
	if err != nil {
		t.Fatalf("BuildConsistencyContext returned error: %v", err)
	}
	if !strings.Contains(got, "Document title: Sample") {
		t.Fatalf("title should be preserved first: %s", got)
	}
	if len(got) > 45 {
		t.Fatalf("context length exceeded: %d", len(got))
	}
}
