package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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
	refineStartTimeRe      = regexp.MustCompile(`^\[?\d{2}:\d{2}:\d{2},\d{3}\]?$`)
	timelineLineRe         = regexp.MustCompile(`^\[?(\d{2}:\d{2}:\d{2},\d{3})\]?\s*(.*)$`)
	numberMarkerRe         = regexp.MustCompile(`^\[(\d+)\][\s:.-]*(.*)$`)
	dottedNumberMarkerRe   = regexp.MustCompile(`^(\d+)[\.\)][\s:.-]*(.*)$`)
	flexibleTimestampRe    = regexp.MustCompile(`^\[?(\d{1,2}:)?(\d{1,2}):(\d{1,2})[,.](\d{1,3})\]?$`)
)

func normalizeTimelineTimestamp(raw string) string {
	raw = strings.Trim(raw, "[] \t")
	raw = strings.ReplaceAll(raw, ".", ",")
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return raw
	}
	timePart := parts[0]
	msPart := parts[1]
	for len(msPart) < 3 {
		msPart += "0"
	}
	if len(msPart) > 3 {
		msPart = msPart[:3]
	}
	timeSegments := strings.Split(timePart, ":")
	var h, m, s int
	if len(timeSegments) == 2 {
		_, _ = fmt.Sscanf(timeSegments[0], "%d", &m)
		_, _ = fmt.Sscanf(timeSegments[1], "%d", &s)
	} else if len(timeSegments) == 3 {
		_, _ = fmt.Sscanf(timeSegments[0], "%d", &h)
		_, _ = fmt.Sscanf(timeSegments[1], "%d", &m)
		_, _ = fmt.Sscanf(timeSegments[2], "%d", &s)
	} else {
		return raw
	}
	return fmt.Sprintf("%02d:%02d:%02d,%s", h, m, s, msPart)
}

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

	numberedLines := make([]string, 0, len(sentences))
	for i, sentence := range sentences {
		numberedLines = append(numberedLines, fmt.Sprintf("[%d] %s", i+1, sentence.Content))
	}
	numberedTimeline := strings.Join(numberedLines, "\n")

	prompt := ""
	if strings.TrimSpace(description) != "" {
		prompt += "[Reference Context]\n\"\"\"\n" + strings.TrimSpace(description) + "\n\"\"\"\n\n"
	}
	prompt += "[Task]\n"
	prompt += "Use only the numbered sentences below. Decide paragraph boundaries and write a short paragraph summary for each boundary.\n"
	prompt += "Return plain text only. Do not return JSON, Markdown, bullets, or explanations.\n"
	prompt += "Each output line must use this exact format: [sentence_number] paragraph summary.\n"
	prompt += "The first output line must start with [1].\n"
	prompt += "Every output sentence number must be a valid number from the input.\n"
	prompt += "Do not include sentence content in the output; only sentence numbers and summaries.\n"
	prompt += "Prefer paragraphs of 4 to 8 sentences. Start a new paragraph on topic changes.\n"
	prompt += "Example output:\n[1] 회의 시작 및 안건 소개\n[6] 세부 논의\n[12] 마무리\n\n"
	prompt += "[Numbered Sentences]\n\"\"\"\n" + normalizeRefineInputText(numberedTimeline) + "\n\"\"\"\n"

	plan, err := r.requestRefine(prompt, systemPrompt, "", nil)
	if err != nil {
		// Fallback to single paragraph containing all polished sentences
		return BuildSingleParagraphRefinedJSON(sentences)
	}
	markers, err := parseParagraphMarkers(plan, sentences)
	if err != nil {
		// Fallback to single paragraph containing all polished sentences
		return BuildSingleParagraphRefinedJSON(sentences)
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
		Temperature: genai.Ptr[float32](0.7),
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
		var normTS string
		var content string
		if m := timelineLineRe.FindStringSubmatch(line); len(m) == 3 {
			normTS = m[1]
			content = m[2]
		} else {
			// Relaxed match for timestamps like [00:15,860] or [00:3:22,270]
			idxOpen := strings.Index(line, "[")
			idxClose := strings.Index(line, "]")
			if idxOpen == 0 && idxClose > idxOpen {
				rawTS := line[idxOpen+1 : idxClose]
				normTS = normalizeTimelineTimestamp(rawTS)
				content = strings.TrimSpace(line[idxClose+1:])
			} else {
				return nil, fmt.Errorf("invalid timeline line %d: %q", i+1, line)
			}
		}
		content = strings.TrimSpace(strings.Trim(content, `"`))
		if content == "" {
			return nil, fmt.Errorf("empty timeline content at line %d", i+1)
		}
		out = append(out, timelineSentence{
			StartTime: "[" + normTS + "]",
			Content:   content,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("empty timeline")
	}
	return out, nil
}

func parseParagraphMarkers(raw string, sentences []timelineSentence) ([]paragraphMarker, error) {
	if len(sentences) == 0 {
		return nil, errors.New("empty sentences")
	}

	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n"), "\n")
	out := []paragraphMarker{}
	lastIndex := -1
	seen := map[int]struct{}{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")

		var targetIdx = -1
		var summary string

		// 1. Primary: match sentence index like [1] summary or 1. summary
		if m := numberMarkerRe.FindStringSubmatch(line); len(m) == 3 {
			num, err := strconv.Atoi(m[1])
			if err == nil {
				targetIdx = num - 1
				summary = strings.TrimSpace(m[2])
			}
		} else if m := dottedNumberMarkerRe.FindStringSubmatch(line); len(m) == 3 {
			num, err := strconv.Atoi(m[1])
			if err == nil {
				targetIdx = num - 1
				summary = strings.TrimSpace(m[2])
			}
		}

		// 2. Secondary fallback: if Gemini outputs a timestamp instead of index
		if targetIdx == -1 {
			if m := timelineLineRe.FindStringSubmatch(line); len(m) == 3 {
				targetIdx = findNearestSentenceIndex(m[1], sentences)
				summary = strings.TrimSpace(m[2])
			} else {
				idxOpen := strings.Index(line, "[")
				idxClose := strings.Index(line, "]")
				if idxOpen == 0 && idxClose > idxOpen {
					normTS := normalizeTimelineTimestamp(line[idxOpen+1 : idxClose])
					targetIdx = findNearestSentenceIndex(normTS, sentences)
					summary = strings.TrimSpace(line[idxClose+1:])
				}
			}
		}

		if targetIdx < 0 {
			continue
		}
		if targetIdx >= len(sentences) {
			targetIdx = len(sentences) - 1
		}
		if _, exists := seen[targetIdx]; exists {
			continue
		}
		if targetIdx <= lastIndex {
			continue
		}

		summary = strings.TrimSpace(strings.TrimPrefix(summary, ":"))
		summary = strings.TrimSpace(strings.TrimPrefix(summary, "-"))
		if summary == "" {
			summary = "문단"
		}

		out = append(out, paragraphMarker{
			StartTime: sentences[targetIdx].StartTime,
			Summary:   summary,
		})
		seen[targetIdx] = struct{}{}
		lastIndex = targetIdx
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

func findNearestSentenceIndex(targetTS string, sentences []timelineSentence) int {
	targetDist := parseTimestampMs(targetTS)
	bestIdx := 0
	bestDiff := int64(1<<62 - 1)
	for i, s := range sentences {
		currDist := parseTimestampMs(s.StartTime)
		diff := targetDist - currDist
		if diff < 0 {
			diff = -diff
		}
		if diff < bestDiff {
			bestDiff = diff
			bestIdx = i
		}
	}
	return bestIdx
}

func parseTimestampMs(ts string) int64 {
	ts = strings.Trim(ts, "[] \t")
	var h, m, s, ms int64
	_, _ = fmt.Sscanf(ts, "%d:%d:%d,%d", &h, &m, &s, &ms)
	return (((h*60)+m)*60+s)*1000 + ms
}

// BuildSingleParagraphRefinedJSON packs all polished sentences into a single paragraph
// to preserve polished sentences without loss when paragraph structuring cannot be completed.
func BuildSingleParagraphRefinedJSON(sentences []timelineSentence) (string, error) {
	result := refineResponse{
		Paragraph: []refineParagraph{
			{
				ParagraphSummary: "전사 내용",
				Sentence:         make([]refineSentence, 0, len(sentences)),
			},
		},
	}
	for _, s := range sentences {
		result.Paragraph[0].Sentence = append(result.Paragraph[0].Sentence, refineSentence{
			StartTime: s.StartTime,
			Content:   s.Content,
		})
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
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
