package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"google.golang.org/genai"
)

// refineResponse is the structured JSON shape requested from Gemini for transcript refinement.
type refineResponse struct {
	Paragraph []refineParagraph `json:"paragraph"`
}

// refineParagraph is one refined paragraph in the Gemini response.
type refineParagraph struct {
	ParagraphSummary string           `json:"paragraph_summary"`
	Sentence         []refineSentence `json:"sentence"`
}

// refineSentence is one refined sentence aligned to its original timestamp.
type refineSentence struct {
	StartTime string `json:"start_time"`
	Content   string `json:"content"`
}

type timelineSentence struct {
	StartTime string
	Content   string
}

type paragraphMarker struct {
	StartTime string
	Summary   string
}

var (
	refineStartTimeRe = regexp.MustCompile(`^\[?\d{2}:\d{2}:\d{2},\d{3}\]?$`)
	timelineLineRe    = regexp.MustCompile(`^\[?(\d{2}:\d{2}:\d{2},\d{3})\]?\s*(.*)$`)
)

const refineRequestTimeout = 180 * time.Second

// PolishTranscriptTimeline preserves timestamped lines while correcting STT text.
func (r *Runtime) PolishTranscriptTimeline(rawText, description string) (string, error) {
	systemPrompt, err := r.refineTimelineSystemPrompt()
	if err != nil {
		return "", err
	}

	prompt := ""
	if strings.TrimSpace(description) != "" {
		prompt += "[Reference Context]\n\"\"\"\n" + strings.TrimSpace(description) + "\n\"\"\"\n\n"
	}
	prompt += "[Task]\n"
	prompt += "Correct the transcript line by line while preserving every spoken detail and the original line order.\n"
	prompt += "Return plain text only. Do not return JSON or Markdown.\n"
	prompt += "Each input line is one timeline item in the format [HH:MM:SS,mmm] text.\n"
	prompt += "Return exactly one output line for each input line, in the same order, beginning with the same bracketed timestamp.\n"
	prompt += "Do not summarize, merge unrelated lines, or omit speech content.\n\n"
	prompt += "[Original Timeline]\n\"\"\"\n" + normalizeRefineInputText(rawText) + "\n\"\"\"\n"

	text, err := r.requestRefine(prompt, systemPrompt, "", nil)
	if err != nil {
		return "", err
	}
	return normalizePolishedTimeline(text)
}

// StructureTranscriptParagraphs converts a polished timestamped timeline to the UI's refined JSON schema.
func (r *Runtime) StructureTranscriptParagraphs(polishedTimeline, description string) (string, error) {
	systemPrompt, err := r.transcriptSystemPrompt()
	if err != nil {
		return "", err
	}
	sentences, err := parseTimelineSentences(polishedTimeline)
	if err != nil {
		return "", err
	}

	prompt := ""
	if strings.TrimSpace(description) != "" {
		prompt += "[Reference Context]\n\"\"\"\n" + strings.TrimSpace(description) + "\n\"\"\"\n\n"
	}
	prompt += "[Task]\n"
	prompt += "Use only the polished timeline below. Decide paragraph boundaries and write a short paragraph summary for each boundary.\n"
	prompt += "Return plain text only. Do not return JSON, Markdown, bullets, numbering, or explanations.\n"
	prompt += "Each output line must use this exact format: [HH:MM:SS,mmm] paragraph summary.\n"
	prompt += "The first output line must start with the first timeline timestamp.\n"
	prompt += "Every output timestamp must be copied exactly from one timeline line.\n"
	prompt += "Do not include sentence content in the output; only paragraph start timestamps and summaries.\n"
	prompt += "Prefer paragraphs of 4 to 8 timeline lines. Start a new paragraph on topic changes.\n\n"
	prompt += "[Polished Timeline]\n\"\"\"\n" + normalizeRefineInputText(polishedTimeline) + "\n\"\"\"\n"

	plan, err := r.requestRefine(prompt, systemPrompt, "", nil)
	if err != nil {
		return "", err
	}
	markers, err := parseParagraphMarkers(plan, sentences)
	if err != nil {
		return "", err
	}
	return buildRefinedJSONFromTimeline(sentences, markers)
}

// RefineTranscript preserves the old single-call API as a wrapper around the two-step flow.
func (r *Runtime) RefineTranscript(rawText, description string) (string, error) {
	polished, err := r.PolishTranscriptTimeline(rawText, description)
	if err != nil {
		return "", err
	}
	return r.StructureTranscriptParagraphs(polished, description)
}

func (r *Runtime) requestRefine(prompt, systemPrompt, responseMIMEType string, responseSchema *genai.Schema) (string, error) {
	r.loadKeys()
	r.mu.Lock()
	clientCount := len(r.clients)
	r.mu.Unlock()
	if clientCount == 0 {
		return "", errors.New("gemini api is not configured")
	}

	var lastErr error = errors.New("gemini request failed")
	maxAttempts := clientCount * 3
	for attempt := 0; attempt < maxAttempts; {
		idx, waitErr := r.waitForReadyClient(context.Background())
		if waitErr != nil {
			return "", waitErr
		}
		attempt++

		text, genErr := r.generateRefine(idx, systemPrompt, responseMIMEType, responseSchema, prompt)
		if genErr == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), nil
		}
		lastErr = genErr
	}
	return "", lastErr
}

// generateRefine performs one refine request using the selected Gemini client.
func (r *Runtime) generateRefine(idx int, systemPrompt, responseMIMEType string, responseSchema *genai.Schema, prompt string) (string, error) {
	r.mu.Lock()
	if idx < 0 || idx >= len(r.clients) {
		r.mu.Unlock()
		return "", errors.New("invalid client index")
	}
	c := r.clients[idx].client
	keySuffix := maskedKeySuffix(r.clients[idx].key)
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), refineRequestTimeout)
	defer cancel()

	cfg := &genai.GenerateContentConfig{
		Temperature: genai.Ptr[float32](0.2),
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemPrompt}},
		},
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel: genai.ThinkingLevelMedium,
		},
	}
	if strings.TrimSpace(responseMIMEType) != "" {
		cfg.ResponseMIMEType = responseMIMEType
	}
	if responseSchema != nil {
		cfg.ResponseSchema = responseSchema
	}

	result, err := c.Models.GenerateContent(
		ctx,
		r.cfg.Model,
		[]*genai.Content{{
			Role:  "user",
			Parts: []*genai.Part{{Text: prompt}},
		}},
		cfg,
	)
	if err != nil {
		r.onFailure(idx, err)
		return "", err
	}
	if result == nil {
		err = errors.New("empty response")
		r.onFailure(idx, err)
		return "", err
	}
	text := strings.TrimSpace(result.Text())
	if text == "" {
		err = emptyResponseError(result)
		r.onFailure(idx, err)
		return "", err
	}
	if responseSchema != nil {
		text, err = normalizeRefineResponseJSON(text)
		if err != nil {
			r.onFailure(idx, err)
			return "", err
		}
	}
	r.onSuccess(idx)
	r.logf("[GEMINI] success api_key_suffix=%s", keySuffix)
	return text, nil
}

// normalizeRefineResponseJSON trims and validates Gemini refine output JSON.
func normalizeRefineResponseJSON(raw string) (string, error) {
	var parsed refineResponse
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", err
	}
	for i := range parsed.Paragraph {
		parsed.Paragraph[i].ParagraphSummary = strings.TrimSpace(parsed.Paragraph[i].ParagraphSummary)
		for j := range parsed.Paragraph[i].Sentence {
			parsed.Paragraph[i].Sentence[j].StartTime = strings.TrimSpace(parsed.Paragraph[i].Sentence[j].StartTime)
			parsed.Paragraph[i].Sentence[j].Content = strings.TrimSpace(parsed.Paragraph[i].Sentence[j].Content)
			if !refineStartTimeRe.MatchString(parsed.Paragraph[i].Sentence[j].StartTime) {
				return "", fmt.Errorf("invalid start_time at paragraph %d sentence %d", i+1, j+1)
			}
		}
	}
	normalized, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}

func normalizePolishedTimeline(raw string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return "", errors.New("empty polished timeline")
	}
	return strings.Join(out, "\n"), nil
}

func parseTimelineSentences(timeline string) ([]timelineSentence, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(timeline), "\r\n", "\n"), "\n")
	out := make([]timelineSentence, 0, len(lines))
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		m := timelineLineRe.FindStringSubmatch(line)
		if len(m) != 3 {
			return nil, fmt.Errorf("invalid timeline line %d: %q", i+1, line)
		}
		content := strings.TrimSpace(strings.Trim(m[2], `"`))
		if content == "" {
			return nil, fmt.Errorf("empty timeline content at line %d", i+1)
		}
		out = append(out, timelineSentence{
			StartTime: "[" + m[1] + "]",
			Content:   content,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("empty timeline")
	}
	return out, nil
}

func parseParagraphMarkers(raw string, sentences []timelineSentence) ([]paragraphMarker, error) {
	valid := make(map[string]int, len(sentences))
	for i, sentence := range sentences {
		valid[sentence.StartTime] = i
	}

	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n"), "\n")
	out := []paragraphMarker{}
	lastIndex := -1
	seen := map[string]struct{}{}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		m := timelineLineRe.FindStringSubmatch(line)
		if len(m) != 3 {
			return nil, fmt.Errorf("invalid paragraph marker line %d: %q", i+1, line)
		}
		startTime := "[" + m[1] + "]"
		index, ok := valid[startTime]
		if !ok {
			return nil, fmt.Errorf("paragraph marker timestamp not found in timeline: %s", startTime)
		}
		if _, exists := seen[startTime]; exists {
			return nil, fmt.Errorf("duplicate paragraph marker timestamp: %s", startTime)
		}
		if index <= lastIndex {
			return nil, fmt.Errorf("paragraph markers are not in timeline order at %s", startTime)
		}
		summary := strings.TrimSpace(m[2])
		if summary == "" {
			summary = "문단"
		}
		out = append(out, paragraphMarker{StartTime: startTime, Summary: summary})
		seen[startTime] = struct{}{}
		lastIndex = index
	}
	if len(out) == 0 {
		return nil, errors.New("empty paragraph marker result")
	}
	if out[0].StartTime != sentences[0].StartTime {
		out = append([]paragraphMarker{{
			StartTime: sentences[0].StartTime,
			Summary:   out[0].Summary,
		}}, out...)
	}
	return out, nil
}

func buildRefinedJSONFromTimeline(sentences []timelineSentence, markers []paragraphMarker) (string, error) {
	markerByTime := make(map[string]string, len(markers))
	for _, marker := range markers {
		markerByTime[marker.StartTime] = marker.Summary
	}

	result := refineResponse{}
	for _, sentence := range sentences {
		if summary, ok := markerByTime[sentence.StartTime]; ok || len(result.Paragraph) == 0 {
			if !ok {
				summary = "문단"
			}
			result.Paragraph = append(result.Paragraph, refineParagraph{
				ParagraphSummary: summary,
			})
		}
		last := len(result.Paragraph) - 1
		result.Paragraph[last].Sentence = append(result.Paragraph[last].Sentence, refineSentence{
			StartTime: sentence.StartTime,
			Content:   sentence.Content,
		})
	}

	normalized, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}
