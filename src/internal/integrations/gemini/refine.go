package gemini

import (
	"context"
	"encoding/json"
	"errors"
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

// RefineTranscript sends transcript text to Gemini and rotates across API keys on failure.
func (r *Runtime) RefineTranscript(rawText, description string) (string, error) {
	r.loadKeys()
	r.mu.Lock()
	clientCount := len(r.clients)
	r.mu.Unlock()
	if clientCount == 0 {
		return "", errors.New("gemini api is not configured")
	}

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
	prompt += "[Original]\n\"\"\"\n" + normalizeRefineInputText(rawText) + "\n\"\"\"\n"

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

		text, genErr := r.generateRefine(idx, systemPrompt, responseSchema, prompt)
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
func (r *Runtime) generateRefine(idx int, systemPrompt string, responseSchema *genai.Schema, prompt string) (string, error) {
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

	result, err := c.Models.GenerateContent(
		ctx,
		r.cfg.Model,
		[]*genai.Content{{
			Role:  "user",
			Parts: []*genai.Part{{Text: prompt}},
		}},
		&genai.GenerateContentConfig{
			Temperature: genai.Ptr[float32](0.5),
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: systemPrompt}},
			},
			ThinkingConfig: &genai.ThinkingConfig{
				ThinkingLevel: genai.ThinkingLevelHigh,
			},
			ResponseMIMEType: "application/json",
			ResponseSchema:   responseSchema,
		},
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
	text, err = normalizeRefineResponseJSON(text)
	if err != nil {
		r.onFailure(idx, err)
		return "", err
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
		}
	}
	normalized, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}
