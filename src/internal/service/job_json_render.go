package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

type transcriptJSON struct {
	Segments []transcriptSegment `json:"segments"`
}

type transcriptSegment struct {
	From string `json:"from"`
	To   string `json:"to"`
	Text string `json:"text"`
}

type refinedJSON struct {
	Paragraph []refinedParagraph `json:"paragraph"`
}

type refinedParagraph struct {
	ParagraphSummary string            `json:"paragraph_summary"`
	Sentence         []refinedSentence `json:"sentence"`
}

type refinedSentence struct {
	StartTime string `json:"start_time"`
	Content   string `json:"content"`
}

type documentJSON struct {
	Pages []documentPage `json:"pages"`
}

type documentPage struct {
	PageIndex int               `json:"page_index"`
	Elements  []documentElement `json:"elements"`
}

type documentElement struct {
	Header *struct {
		Level int    `json:"level"`
		Text  string `json:"text"`
	} `json:"header"`
	Text       string `json:"text"`
	MathInline string `json:"math_inline"`
	MathBlock  string `json:"math_block"`
	List       *struct {
		Ordered bool               `json:"ordered"`
		Items   []documentListItem `json:"items"`
	} `json:"list"`
	Img *struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"img"`
	Table *struct {
		Title string `json:"title"`
		Rows  []struct {
			Cells []string `json:"cells"`
		} `json:"rows"`
	} `json:"table"`
}

type documentListItem struct {
	Text     string             `json:"text"`
	Children []documentListItem `json:"children"`
}

func RenderTranscriptTimelineText(raw string) (string, error) {
	var parsed transcriptJSON
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", err
	}
	lines := make([]string, 0, len(parsed.Segments))
	for _, segment := range parsed.Segments {
		text := strings.TrimSpace(segment.Text)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf(`%s ~ %s "%s"`, segment.From, segment.To, text))
	}
	return strings.Join(lines, "\n"), nil
}

func RenderTranscriptMarkdown(raw string) (string, error) {
	var parsed transcriptJSON
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", err
	}
	lines := make([]string, 0, len(parsed.Segments)*2)
	for _, segment := range parsed.Segments {
		text := strings.TrimSpace(segment.Text)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- [%s ~ %s] %s", segment.From, segment.To, text))
	}
	return strings.Join(lines, "\n"), nil
}

func RenderRefinedMarkdown(raw string) (string, error) {
	var parsed refinedJSON
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", err
	}
	lines := []string{}
	for _, paragraph := range parsed.Paragraph {
		summary := strings.TrimSpace(paragraph.ParagraphSummary)
		if summary != "" {
			lines = append(lines, "### "+summary, "")
		}
		for _, sentence := range paragraph.Sentence {
			content := strings.TrimSpace(sentence.Content)
			if content == "" {
				continue
			}
			startTime := strings.TrimSpace(sentence.StartTime)
			if startTime != "" {
				lines = append(lines, fmt.Sprintf("- %s %s", startTime, content))
			} else {
				lines = append(lines, "- "+content)
			}
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

func RenderDocumentMarkdown(raw string) (string, error) {
	var parsed documentJSON
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", err
	}
	lines := []string{}
	for _, page := range parsed.Pages {
		lines = append(lines, fmt.Sprintf("## Page %d", page.PageIndex), "")
		for _, element := range page.Elements {
			switch {
			case element.Header != nil:
				level := element.Header.Level
				if level < 1 {
					level = 1
				}
				if level > 3 {
					level = 3
				}
				lines = append(lines, strings.Repeat("#", level)+" "+strings.TrimSpace(element.Header.Text), "")
			case strings.TrimSpace(element.MathBlock) != "":
				lines = append(lines, "$$", strings.TrimSpace(element.MathBlock), "$$", "")
			case strings.TrimSpace(element.MathInline) != "":
				lines = append(lines, "$"+strings.TrimSpace(element.MathInline)+"$", "")
			case strings.TrimSpace(element.Text) != "":
				lines = append(lines, strings.TrimSpace(element.Text), "")
			case element.List != nil && len(element.List.Items) > 0:
				lines = append(lines, renderDocumentListMarkdown(element.List.Items, element.List.Ordered, 0)...)
				lines = append(lines, "")
			case element.Img != nil:
				lines = append(lines, "**"+strings.TrimSpace(element.Img.Title)+"**", "", strings.TrimSpace(element.Img.Description), "")
			case element.Table != nil:
				lines = append(lines, "**"+strings.TrimSpace(element.Table.Title)+"**", "")
				lines = append(lines, renderDocumentTableMarkdown(element.Table.Rows)...)
				lines = append(lines, "")
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

func renderDocumentListMarkdown(items []documentListItem, ordered bool, depth int) []string {
	lines := []string{}
	for i, item := range items {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		marker := "-"
		if ordered {
			marker = fmt.Sprintf("%d.", i+1)
		}
		lines = append(lines, strings.Repeat("  ", depth)+marker+" "+text)
		if len(item.Children) > 0 {
			lines = append(lines, renderDocumentListMarkdown(item.Children, false, depth+1)...)
		}
	}
	return lines
}

func renderDocumentTableMarkdown(rows []struct {
	Cells []string `json:"cells"`
}) []string {
	if len(rows) == 0 {
		return nil
	}
	header := rows[0].Cells
	divider := make([]string, len(header))
	for i := range divider {
		divider[i] = "---"
	}
	lines := []string{
		"| " + strings.Join(header, " | ") + " |",
		"| " + strings.Join(divider, " | ") + " |",
	}
	for _, row := range rows[1:] {
		lines = append(lines, "| "+strings.Join(row.Cells, " | ")+" |")
	}
	return lines
}
