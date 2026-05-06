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

var refineStartTimeRe = regexp.MustCompile(`^\[?\d{2}:\d{2}:\d{2},\d{3}\]?$`)

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
	prompt += "Each output line must begin with the original timestamp or timestamp range from the corresponding input line.\n"
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
	responseSchema, err := r.transcriptResponseSchema()
	if err != nil {
		return "", err
	}

	prompt := ""
	if strings.TrimSpace(description) != "" {
		prompt += "[Reference Context]\n\"\"\"\n" + strings.TrimSpace(description) + "\n\"\"\"\n\n"
	}
	prompt += "[Task]\n"
	prompt += "Use only the polished timeline below. Build paragraphs in the existing response schema.\n"
	prompt += "Every sentence start_time must come from the polished timeline timestamps.\n"
	prompt += "Do not summarize away content or invent timestamps.\n\n"
	prompt += "[Polished Timeline]\n\"\"\"\n" + normalizeRefineInputText(polishedTimeline) + "\n\"\"\"\n"

	return r.requestRefine(prompt, systemPrompt, "application/json", responseSchema)
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
	for attempt := 0; attempt < maxAttempts; attempt++ {
		idx, waitFor := r.nextReadyClient(time.Now())
		if idx < 0 {
			if waitFor > 3*time.Second {
				waitFor = 3 * time.Second
			}
			if waitFor > 0 {
				time.Sleep(waitFor)
			}
			continue
		}

		text, genErr := r.generateRefine(idx, systemPrompt, responseMIMEType, responseSchema, prompt)
		if genErr == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), nil
		}
		lastErr = genErr
	}
	r.mu.Lock()
	for i := range r.clients {
		if r.clients[i].failCount > 0 {
			r.clients[i].cooldownUntil = time.Time{}
		}
	}
	r.mu.Unlock()
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

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cfg := &genai.GenerateContentConfig{
		Temperature: genai.Ptr[float32](0.7),
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemPrompt}},
		},
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel: genai.ThinkingLevelHigh,
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
		err = errors.New("empty response text")
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
