package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
	"whisperserver/src/internal/worker"
)

type documentElement struct {
	Header *struct {
		Level int    `json:"level"`
		Text  string `json:"text"`
	} `json:"header,omitempty"`
	Text       string   `json:"text,omitempty"`
	MathInline string   `json:"math_inline,omitempty"`
	MathBlock  string   `json:"math_block,omitempty"`
	List       []string `json:"list,omitempty"`
	Img        *struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"img,omitempty"`
	Table *struct {
		Title string `json:"title"`
		Rows  []struct {
			Cells []string `json:"cells"`
		} `json:"rows"`
	} `json:"table,omitempty"`
}

type documentPage struct {
	PageIndex int               `json:"page_index"`
	Elements  []documentElement `json:"elements"`
}

type documentResponse struct {
	Pages []documentPage `json:"pages"`
}

var (
	documentPromptOnce sync.Once
	documentPromptText string
	documentPromptErr  error
	latexEnvironmentRe = regexp.MustCompile(`\\begin\{[a-zA-Z*]+\}[\s\S]*?\\end\{[a-zA-Z*]+\}`)
	inlineMathTokenRe  = regexp.MustCompile(`(?:[A-Za-z0-9]+[A-Za-z0-9_\\^{}()&,+\-*/=≠≤≥]+[A-Za-z0-9_\\^{}()&,+\-*/=≠≤≥]*|[A-Za-z]+_[A-Za-z0-9]+)`)
	hangulRe           = regexp.MustCompile(`[가-힣]`)
)

const documentResponseSchemaJSON = `{
  "type": "object",
  "properties": {
    "pages": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "page_index": {
            "type": "integer"
          },
          "elements": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "header": {
                  "type": "object",
                  "properties": {
                    "level": {
                      "type": "integer"
                    },
                    "text": {
                      "type": "string"
                    }
                  },
                  "required": [
                    "level",
                    "text"
                  ]
                },
                "text": {
                  "type": "string"
                },
                "math_inline": {
                  "type": "string"
                },
                "math_block": {
                  "type": "string"
                },
                "img": {
                  "type": "object",
                  "properties": {
                    "title": {
                      "type": "string"
                    },
                    "description": {
                      "type": "string"
                    }
                  },
                  "required": [
                    "title",
                    "description"
                  ]
                },
                "list": {
                  "type": "array",
                  "items": {
                    "type": "string"
                  }
                },
                "table": {
                  "type": "object",
                  "properties": {
                    "title": {
                      "type": "string"
                    },
                    "rows": {
                      "type": "array",
                      "items": {
                        "type": "object",
                        "properties": {
                          "cells": {
                            "type": "array",
                            "items": {
                              "type": "string"
                            }
                          }
                        },
                        "required": [
                          "cells"
                        ]
                      }
                    }
                  },
                  "required": [
                    "title",
                    "rows"
                  ]
                }
              }
            }
          }
        },
        "required": [
          "page_index",
          "elements"
        ]
      }
    }
  },
  "required": [
    "pages"
  ]
}`

func extractDocumentChunk(ctx context.Context, chunk worker.DocumentChunk, consistencyContext string) ([]byte, error) {
	gClient.loadKeys()
	gClient.mu.Lock()
	clientCount := len(gClient.clients)
	gClient.mu.Unlock()
	if clientCount == 0 {
		return nil, errors.New("Gemini API is not configured")
	}

	systemPrompt, err := documentSystemPrompt()
	if err != nil {
		return nil, err
	}
	schema, err := parseDocumentResponseSchema()
	if err != nil {
		return nil, err
	}

	instructionText := buildDocumentChunkPrompt(chunk, consistencyContext)
	var lastErr error = errors.New("gemini document request failed")
	maxAttempts := clientCount * 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		idx, waitFor := gClient.nextReadyClient(time.Now())
		if idx < 0 {
			if waitFor > 3*time.Second {
				waitFor = 3 * time.Second
			}
			if waitFor > 0 {
				time.Sleep(waitFor)
			}
			continue
		}
		result, genErr := gClient.generateDocument(ctx, idx, systemPrompt, schema, instructionText, chunk)
		if genErr == nil && len(result) > 0 {
			return result, nil
		}
		lastErr = genErr
	}
	return nil, lastErr
}

func buildDocumentChunkPrompt(chunk worker.DocumentChunk, consistencyContext string) string {
	lines := []string{
		"[Document Batch Info]",
		fmt.Sprintf("- Total pages: %d", chunk.TotalPages),
		fmt.Sprintf("- Current chunk: %d/%d", chunk.ChunkIndex, chunk.TotalChunks),
		fmt.Sprintf("- Current page range: %d-%d", chunk.StartPage, chunk.EndPage),
		"",
		"[Instructions]",
		"- This request may contain only part of the full document.",
		"- Return only the pages included in this batch.",
		"- Keep terminology and heading hierarchy consistent with previous chunks when possible.",
		"- If the current page evidence conflicts with previous context, trust the current page.",
	}
	if strings.TrimSpace(consistencyContext) != "" {
		lines = append(lines,
			"",
			"[Consistency Context From Previous Chunks]",
			strings.TrimSpace(consistencyContext),
		)
	}
	return strings.Join(lines, "\n")
}

func (g *geminiClient) generateDocument(ctx context.Context, idx int, systemPrompt string, schema *genai.Schema, instruction string, chunk worker.DocumentChunk) ([]byte, error) {
	g.mu.Lock()
	if idx < 0 || idx >= len(g.clients) {
		g.mu.Unlock()
		return nil, errors.New("invalid client index")
	}
	c := g.clients[idx].client
	keySuffix := maskedKeySuffix(g.clients[idx].key)
	g.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(pdfBatchTimeoutSec)*time.Second)
	defer cancel()

	parts := []*genai.Part{genai.NewPartFromText(instruction)}
	for _, image := range chunk.Images {
		parts = append(parts, genai.NewPartFromText(fmt.Sprintf("Page %d", image.PageIndex)))
		parts = append(parts, genai.NewPartFromBytes(image.Data, image.MIMEType))
	}

	result, err := c.Models.GenerateContent(
		callCtx,
		geminiModel,
		[]*genai.Content{{
			Role:  "user",
			Parts: parts,
		}},
		&genai.GenerateContentConfig{
			Temperature: genai.Ptr[float32](0.2),
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: systemPrompt}},
			},
			ThinkingConfig: &genai.ThinkingConfig{
				ThinkingLevel: genai.ThinkingLevelHigh,
			},
			ResponseMIMEType: "application/json",
			ResponseSchema:   schema,
		},
	)
	if err != nil {
		g.onFailure(idx, err)
		return nil, err
	}
	if result == nil || strings.TrimSpace(result.Text()) == "" {
		err = errors.New("empty response text")
		g.onFailure(idx, err)
		return nil, err
	}
	normalized, err := normalizeDocumentResponseJSON(result.Text())
	if err != nil {
		g.onFailure(idx, err)
		return nil, err
	}
	normalized, err = alignDocumentPageIndexes(normalized, chunk)
	if err != nil {
		g.onFailure(idx, err)
		return nil, err
	}
	g.onSuccess(idx)
	procLogf("[GEMINI] document success api_key_suffix=%s chunk=%d/%d", keySuffix, chunk.ChunkIndex, chunk.TotalChunks)
	return normalized, nil
}

func documentSystemPrompt() (string, error) {
	documentPromptOnce.Do(func() {
		promptPath := filepath.Join(projectRoot, "docs", "prompts", "file_transcript_system_prompt.md")
		b, err := os.ReadFile(promptPath)
		if err != nil {
			documentPromptErr = err
			return
		}
		documentPromptText = strings.TrimSpace(string(b)) + "\n\nAdditional rules:\n- Treat each image as a PDF page.\n- The request may contain only a subset of the document pages.\n- Preserve terminology and heading structure consistently across batches.\n- Return only the pages included in the current batch."
	})
	return documentPromptText, documentPromptErr
}

func parseDocumentResponseSchema() (*genai.Schema, error) {
	var schema genai.Schema
	if err := json.Unmarshal([]byte(documentResponseSchemaJSON), &schema); err != nil {
		return nil, err
	}
	return &schema, nil
}

func normalizeDocumentResponseJSON(raw string) ([]byte, error) {
	var parsed documentResponse
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, err
	}
	for i := range parsed.Pages {
		if parsed.Pages[i].PageIndex <= 0 {
			parsed.Pages[i].PageIndex = i + 1
		}
		for j := range parsed.Pages[i].Elements {
			if parsed.Pages[i].Elements[j].Header != nil {
				parsed.Pages[i].Elements[j].Header.Text = strings.TrimSpace(parsed.Pages[i].Elements[j].Header.Text)
			}
			parsed.Pages[i].Elements[j].Text = strings.TrimSpace(parsed.Pages[i].Elements[j].Text)
			parsed.Pages[i].Elements[j].MathInline = strings.TrimSpace(parsed.Pages[i].Elements[j].MathInline)
			parsed.Pages[i].Elements[j].MathBlock = strings.TrimSpace(parsed.Pages[i].Elements[j].MathBlock)
			for k := range parsed.Pages[i].Elements[j].List {
				parsed.Pages[i].Elements[j].List[k] = strings.TrimSpace(parsed.Pages[i].Elements[j].List[k])
			}
			if parsed.Pages[i].Elements[j].Img != nil {
				parsed.Pages[i].Elements[j].Img.Title = strings.TrimSpace(parsed.Pages[i].Elements[j].Img.Title)
				parsed.Pages[i].Elements[j].Img.Description = strings.TrimSpace(parsed.Pages[i].Elements[j].Img.Description)
			}
			if parsed.Pages[i].Elements[j].Table != nil {
				parsed.Pages[i].Elements[j].Table.Title = strings.TrimSpace(parsed.Pages[i].Elements[j].Table.Title)
				for rowIdx := range parsed.Pages[i].Elements[j].Table.Rows {
					for cellIdx := range parsed.Pages[i].Elements[j].Table.Rows[rowIdx].Cells {
						parsed.Pages[i].Elements[j].Table.Rows[rowIdx].Cells[cellIdx] = strings.TrimSpace(parsed.Pages[i].Elements[j].Table.Rows[rowIdx].Cells[cellIdx])
					}
				}
			}
		}
	}
	return json.MarshalIndent(parsed, "", "  ")
}

func alignDocumentPageIndexes(raw []byte, chunk worker.DocumentChunk) ([]byte, error) {
	var parsed documentResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	for i := range parsed.Pages {
		if i < len(chunk.Images) {
			parsed.Pages[i].PageIndex = chunk.Images[i].PageIndex
		}
	}
	return json.MarshalIndent(parsed, "", "  ")
}

func buildConsistencyContext(raw []byte) (string, error) {
	var doc documentResponse
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	title := ""
	headers := make([]string, 0, 12)
	terms := make([]string, 0, 12)
	for _, page := range doc.Pages {
		for _, el := range page.Elements {
			if el.Header != nil {
				if el.Header.Level == 1 && title == "" {
					title = el.Header.Text
				}
				headers = appendIfMissing(headers, el.Header.Text, 8)
			}
			if el.Img != nil && strings.TrimSpace(el.Img.Title) != "" {
				terms = appendIfMissing(terms, el.Img.Title, 8)
			}
			if el.Table != nil && strings.TrimSpace(el.Table.Title) != "" {
				terms = appendIfMissing(terms, el.Table.Title, 8)
			}
		}
	}
	lines := []string{}
	if title != "" {
		lines = append(lines, "Document title: "+title)
	}
	if len(headers) > 0 {
		lines = append(lines, "Observed headers: "+strings.Join(headers, " | "))
	}
	if len(terms) > 0 {
		lines = append(lines, "Important titles/terms: "+strings.Join(terms, " | "))
	}
	if len(lines) == 0 {
		lines = append(lines, "Keep terminology and heading depth consistent with previous pages.")
	}
	return truncateConsistencyContext(lines, pdfConsistencyContextMaxChars), nil
}

func mergeDocumentJSON(blobs ...[]byte) ([]byte, error) {
	merged := documentResponse{Pages: make([]documentPage, 0)}
	seen := map[int]struct{}{}
	for _, blob := range blobs {
		if len(blob) == 0 {
			continue
		}
		var item documentResponse
		if err := json.Unmarshal(blob, &item); err != nil {
			return nil, err
		}
		for _, page := range item.Pages {
			if _, ok := seen[page.PageIndex]; ok {
				return nil, fmt.Errorf("duplicate page index: %d", page.PageIndex)
			}
			seen[page.PageIndex] = struct{}{}
			merged.Pages = append(merged.Pages, page)
		}
	}
	return json.MarshalIndent(merged, "", "  ")
}

func renderDocumentMarkdown(raw []byte) (string, error) {
	var doc documentResponse
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	lines := make([]string, 0, len(doc.Pages)*8)
	for idx, page := range doc.Pages {
		lines = append(lines, fmt.Sprintf("## Page %d", page.PageIndex), "")
		for _, el := range page.Elements {
			switch {
			case el.Header != nil:
				level := el.Header.Level
				if level < 1 {
					level = 1
				}
				if level > 3 {
					level = 3
				}
				lines = append(lines, strings.Repeat("#", level)+" "+el.Header.Text, "")
			case strings.TrimSpace(el.MathBlock) != "":
				lines = append(lines, "$$", strings.TrimSpace(el.MathBlock), "$$", "")
			case strings.TrimSpace(el.MathInline) != "":
				lines = append(lines, "$"+strings.TrimSpace(el.MathInline)+"$", "")
			case strings.TrimSpace(el.Text) != "":
				lines = append(lines, formatDocumentTextForMarkdown(el.Text), "")
			case len(el.List) > 0:
				for _, item := range el.List {
					if strings.TrimSpace(item) == "" {
						continue
					}
					lines = append(lines, "- "+formatDocumentTextForMarkdown(item))
				}
				lines = append(lines, "")
			case el.Img != nil:
				lines = append(lines, fmt.Sprintf("**%s**", formatDocumentTextForMarkdown(el.Img.Title)), "", formatDocumentTextForMarkdown(el.Img.Description), "")
			case el.Table != nil:
				lines = append(lines, fmt.Sprintf("**%s**", el.Table.Title), "")
				lines = append(lines, renderMarkdownTable(el.Table.Rows)...)
				lines = append(lines, "")
			}
		}
		if idx < len(doc.Pages)-1 {
			lines = append(lines, "---", "")
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

func renderMarkdownTable(rows []struct {
	Cells []string `json:"cells"`
}) []string {
	if len(rows) == 0 {
		return nil
	}
	header := rows[0].Cells
	out := []string{"| " + strings.Join(header, " | ") + " |"}
	divider := make([]string, len(header))
	for i := range divider {
		divider[i] = "---"
	}
	out = append(out, "| "+strings.Join(divider, " | ")+" |")
	for _, row := range rows[1:] {
		out = append(out, "| "+strings.Join(row.Cells, " | ")+" |")
	}
	return out
}

func appendIfMissing(items []string, value string, max int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	if len(items) >= max {
		return items
	}
	return append(items, value)
}

func truncateConsistencyContext(lines []string, maxChars int) string {
	if maxChars <= 0 {
		return strings.Join(lines, "\n")
	}
	joined := strings.Join(lines, "\n")
	if len(joined) <= maxChars {
		return joined
	}
	out := make([]string, 0, len(lines))
	used := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		extra := len(line)
		if len(out) > 0 {
			extra++
		}
		if used+extra > maxChars {
			remaining := maxChars - used
			if remaining > 4 {
				if len(out) > 0 {
					remaining--
				}
				out = append(out, line[:remaining-3]+"...")
			}
			break
		}
		out = append(out, line)
		used += extra
	}
	if len(out) == 0 {
		if maxChars <= 3 {
			return joined[:maxChars]
		}
		return joined[:maxChars-3] + "..."
	}
	return strings.Join(out, "\n")
}

func formatDocumentTextForMarkdown(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if shouldRenderAsDisplayMath(text) {
		return "$$\n" + text + "\n$$"
	}
	text = latexEnvironmentRe.ReplaceAllStringFunc(text, func(match string) string {
		return "$" + match + "$"
	})
	return wrapInlineMathTokens(text)
}

func shouldRenderAsDisplayMath(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || hangulRe.MatchString(text) {
		return false
	}
	if !strings.Contains(text, `\begin{`) {
		return false
	}
	if strings.Contains(text, ",") && !strings.Contains(text, "=") {
		return false
	}
	return strings.Contains(text, "=") || strings.HasPrefix(text, `\begin{`)
}

func wrapInlineMathTokens(text string) string {
	segments := splitByMathDelimiters(text)
	for i := range segments {
		if segments[i].isMath {
			continue
		}
		segments[i].text = inlineMathTokenRe.ReplaceAllStringFunc(segments[i].text, func(token string) string {
			if !shouldWrapMathToken(token) {
				return token
			}
			return "$" + token + "$"
		})
	}
	var b strings.Builder
	for _, segment := range segments {
		b.WriteString(segment.text)
	}
	return b.String()
}

type mathSegment struct {
	text   string
	isMath bool
}

func splitByMathDelimiters(text string) []mathSegment {
	out := make([]mathSegment, 0, 4)
	for len(text) > 0 {
		start := strings.Index(text, "$")
		if start < 0 {
			out = append(out, mathSegment{text: text})
			break
		}
		if start > 0 {
			out = append(out, mathSegment{text: text[:start]})
		}
		end := strings.Index(text[start+1:], "$")
		if end < 0 {
			out = append(out, mathSegment{text: text[start:]})
			break
		}
		end += start + 2
		out = append(out, mathSegment{text: text[start:end], isMath: true})
		text = text[end:]
	}
	return out
}

func shouldWrapMathToken(token string) bool {
	if strings.HasPrefix(token, "$") && strings.HasSuffix(token, "$") {
		return false
	}
	if strings.Contains(token, `\begin{`) {
		return false
	}
	return strings.ContainsAny(token, "_\\^=≠≤≥+-*/&") || containsMathDigitMix(token)
}

func containsMathDigitMix(token string) bool {
	hasLetter := false
	hasDigit := false
	for _, r := range token {
		switch {
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= 'a' && r <= 'z':
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
		if hasLetter && hasDigit {
			return true
		}
	}
	return false
}
