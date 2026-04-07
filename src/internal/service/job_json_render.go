package service

import (
	"encoding/json"
	"strings"

	"whisperserver/src/internal/structured"
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
		lines = append(lines, segment.From+" ~ "+segment.To+` "`+text+`"`)
	}
	return strings.Join(lines, "\n"), nil
}

func RenderTranscriptMarkdown(raw string) (string, error) {
	var parsed transcriptJSON
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", err
	}
	sentences := make([]string, 0, len(parsed.Segments))
	for _, segment := range parsed.Segments {
		text := strings.TrimSpace(segment.Text)
		if text == "" {
			continue
		}
		sentences = append(sentences, text)
	}
	return strings.Join(sentences, " "), nil
}

func RenderRefinedMarkdown(raw string) (string, error) {
	var parsed refinedJSON
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", err
	}
	sections := make([]string, 0, len(parsed.Paragraph))
	for _, paragraph := range parsed.Paragraph {
		summary := strings.TrimSpace(paragraph.ParagraphSummary)
		sentences := make([]string, 0, len(paragraph.Sentence))
		for _, sentence := range paragraph.Sentence {
			content := strings.TrimSpace(sentence.Content)
			if content == "" {
				continue
			}
			sentences = append(sentences, content)
		}
		if summary == "" && len(sentences) == 0 {
			continue
		}
		section := strings.Builder{}
		if summary != "" {
			section.WriteString("## ")
			section.WriteString(summary)
		}
		if len(sentences) > 0 {
			if section.Len() > 0 {
				section.WriteString("\n\n")
			}
			section.WriteString(strings.Join(sentences, " "))
		}
		sections = append(sections, section.String())
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n")), nil
}

func RenderDocumentMarkdown(raw string) (string, error) {
	return structured.RenderDocumentMarkdown([]byte(raw))
}

func RenderDownloadMarkdownTitle(title, body string) string {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" {
		return body
	}
	if body == "" {
		return "# " + title
	}
	return "# " + title + "\n\n" + body
}
