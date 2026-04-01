package app

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

type geminiClient struct {
	mu      sync.Mutex
	clients []geminiKeyClient
	index   int
	load    sync.Once
}

type geminiKeyClient struct {
	key           string
	client        *genai.Client
	failCount     int
	cooldownUntil time.Time
}

var gClient geminiClient
var legacyTimelineLineRe = regexp.MustCompile(`^\[(\d{2}:\d{2}:\d{2})\.(\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2})\.(\d{3})\]\s*(.*)$`)

func (g *geminiClient) loadKeys() {
	g.load.Do(func() {
		seen := map[string]struct{}{}
		add := func(k string) {
			k = strings.TrimSpace(k)
			if k == "" {
				return
			}
			if _, ok := seen[k]; ok {
				return
			}
			seen[k] = struct{}{}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c, err := genai.NewClient(ctx, &genai.ClientConfig{
				APIKey:  k,
				Backend: genai.BackendGeminiAPI,
			})
			if err != nil {
				procErrf("gemini.newClient", err, "api_key_suffix=%s", maskedKeySuffix(k))
				return
			}
			g.clients = append(g.clients, geminiKeyClient{key: k, client: c})
		}
		for _, k := range geminiAPIKeysFromConfig() {
			add(k)
		}
		procLogf("[GEMINI] initialized clients=%d", len(g.clients))
	})
}

func hasGeminiConfigured() bool {
	gClient.loadKeys()
	return len(gClient.clients) > 0
}

func refineTranscript(rawText, description string) (string, error) {
	gClient.loadKeys()
	gClient.mu.Lock()
	clientCount := len(gClient.clients)
	gClient.mu.Unlock()
	if clientCount == 0 {
		return "", errors.New("Gemini API is not configured")
	}

	prompt := "[Original]\n\"\"\"\n" + normalizeRefineInputText(rawText) + "\n\"\"\"\n\n"
	if strings.TrimSpace(description) != "" {
		prompt += "[Reference Context]\n\"\"\"\n" + strings.TrimSpace(description) + "\n\"\"\"\n\n"
	}

	var lastErr error = errors.New("gemini request failed")
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

		text, err := gClient.generate(idx, prompt)
		if err == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), nil
		}
		lastErr = err
	}
	gClient.mu.Lock()
	for i := range gClient.clients {
		if gClient.clients[i].failCount > 0 {
			gClient.clients[i].cooldownUntil = time.Time{}
		}
	}
	gClient.mu.Unlock()
	return "", lastErr
}

func (g *geminiClient) nextReadyClient(now time.Time) (int, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.clients) == 0 {
		return -1, 0
	}

	start := g.index
	var minWait time.Duration
	for i := 0; i < len(g.clients); i++ {
		idx := (start + i) % len(g.clients)
		wait := g.clients[idx].cooldownUntil.Sub(now)
		if wait <= 0 {
			g.index = (idx + 1) % len(g.clients)
			return idx, 0
		}
		if minWait == 0 || wait < minWait {
			minWait = wait
		}
	}
	return -1, minWait
}

func (g *geminiClient) generate(idx int, prompt string) (string, error) {
	g.mu.Lock()
	if idx < 0 || idx >= len(g.clients) {
		g.mu.Unlock()
		return "", errors.New("invalid client index")
	}
	c := g.clients[idx].client
	keySuffix := maskedKeySuffix(g.clients[idx].key)
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	responseSchema, err := parseRefineResponseSchema()
	if err != nil {
		g.onFailure(idx, err)
		return "", err
	}
	result, err := c.Models.GenerateContent(
		ctx,
		geminiModel,
		[]*genai.Content{
			{
				Role: "user",
				Parts: []*genai.Part{
					{Text: prompt},
				},
			},
		},
		&genai.GenerateContentConfig{
			Temperature: genai.Ptr[float32](0.5),
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{
					{Text: refineSystemPrompt},
				},
			},
			ThinkingConfig: &genai.ThinkingConfig{
				ThinkingLevel: genai.ThinkingLevelHigh,
			},
			ResponseMIMEType: "application/json",
			ResponseSchema:   responseSchema,
		},
	)
	if err != nil {
		g.onFailure(idx, err)
		return "", err
	}
	if result == nil {
		err = errors.New("empty response")
		g.onFailure(idx, err)
		return "", err
	}
	text := strings.TrimSpace(result.Text())
	if text == "" {
		err = errors.New("empty response text")
		g.onFailure(idx, err)
		return "", err
	}
	text, err = normalizeRefineResponseJSON(text)
	if err != nil {
		g.onFailure(idx, err)
		return "", err
	}
	g.onSuccess(idx)
	procLogf("[GEMINI] success api_key_suffix=%s", keySuffix)
	return text, nil
}

const refineResponseSchemaJSON = `{
  "type": "object",
  "properties": {
    "paragraph": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "paragraph_summary": {
            "type": "string"
          },
          "sentence": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "start_time": {
                  "type": "string"
                },
                "content": {
                  "type": "string"
                }
              },
              "required": [
                "start_time",
                "content"
              ],
              "propertyOrdering": [
                "start_time",
                "content"
              ]
            }
          }
        },
        "required": [
          "paragraph_summary",
          "sentence"
        ],
        "propertyOrdering": [
          "paragraph_summary",
          "sentence"
        ]
      }
    }
  },
  "required": [
    "paragraph"
  ],
  "propertyOrdering": [
    "paragraph"
  ]
}`

type refineResponse struct {
	Paragraph []refineParagraph `json:"paragraph"`
}

type refineParagraph struct {
	ParagraphSummary string           `json:"paragraph_summary"`
	Sentence         []refineSentence `json:"sentence"`
}

type refineSentence struct {
	StartTime string `json:"start_time"`
	Content   string `json:"content"`
}

func parseRefineResponseSchema() (*genai.Schema, error) {
	var schema genai.Schema
	if err := json.Unmarshal([]byte(refineResponseSchemaJSON), &schema); err != nil {
		return nil, err
	}
	return &schema, nil
}

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

func normalizeRefineInputText(raw string) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if legacy := legacyTimelineLineRe.FindStringSubmatch(line); len(legacy) == 6 {
			out = append(out, legacy[1]+","+legacy[2]+" ~ "+legacy[3]+","+legacy[4]+` "`+strings.TrimSpace(legacy[5])+`"`)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func (g *geminiClient) onSuccess(idx int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if idx < 0 || idx >= len(g.clients) {
		return
	}
	g.clients[idx].failCount = 0
	g.clients[idx].cooldownUntil = time.Time{}
}

func (g *geminiClient) onFailure(idx int, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if idx < 0 || idx >= len(g.clients) {
		return
	}

	base := 2 * time.Second
	c := &g.clients[idx]
	c.failCount++
	backoff := base * time.Duration(1<<(min(c.failCount-1, 4)))
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	if !isRetryableGeminiError(err) {
		backoff = 5 * time.Second
	}
	c.cooldownUntil = time.Now().Add(backoff)
	procErrf("gemini.generate", err, "api_key_suffix=%s cooldown=%s fail_count=%d", maskedKeySuffix(c.key), backoff, c.failCount)
}

func isRetryableGeminiError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate") || strings.Contains(msg, "quota") {
		return true
	}
	if strings.Contains(msg, "500") || strings.Contains(msg, "502") || strings.Contains(msg, "503") || strings.Contains(msg, "504") {
		return true
	}
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "tempor") || strings.Contains(msg, "unavailable") {
		return true
	}
	return false
}

func maskedKeySuffix(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return "-"
	}
	if len(k) <= 4 {
		return k
	}
	return k[len(k)-4:]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
