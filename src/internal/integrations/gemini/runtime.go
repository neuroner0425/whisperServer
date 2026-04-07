package gemini

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

// Config defines Gemini-specific runtime settings and callbacks.
type Config struct {
	Model                         string
	APIKeys                       []string
	PDFBatchTimeoutSec            int
	PDFConsistencyContextMaxChars int
	PromptPath                    string
	Logf                          func(format string, args ...any)
	Errf                          func(scope string, err error, format string, args ...any)
}

// Runtime owns Gemini clients, key rotation, and request helpers.
type Runtime struct {
	cfg Config
	mu  sync.Mutex

	clients []geminiKeyClient
	index   int
	load    sync.Once

	documentPromptOnce sync.Once
	documentPromptText string
	documentPromptErr  error
}

// geminiKeyClient tracks one API key together with cooldown state.
type geminiKeyClient struct {
	key           string
	client        *genai.Client
	failCount     int
	cooldownUntil time.Time
}

var legacyTimelineLineRe = regexp.MustCompile(`^\[(\d{2}:\d{2}:\d{2})\.(\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2})\.(\d{3})\]\s*(.*)$`)

// New creates a Gemini runtime with lazy client initialization.
func New(cfg Config) *Runtime {
	return &Runtime{cfg: cfg}
}

// logf emits integration logs when a logger is configured.
func (r *Runtime) logf(format string, args ...any) {
	if r.cfg.Logf != nil {
		r.cfg.Logf(format, args...)
	}
}

// errf emits integration errors when an error logger is configured.
func (r *Runtime) errf(scope string, err error, format string, args ...any) {
	if r.cfg.Errf != nil {
		r.cfg.Errf(scope, err, format, args...)
	}
}

// loadKeys initializes one Gemini client per unique configured API key.
func (r *Runtime) loadKeys() {
	r.load.Do(func() {
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
				r.errf("gemini.newClient", err, "api_key_suffix=%s", maskedKeySuffix(k))
				return
			}
			r.clients = append(r.clients, geminiKeyClient{key: k, client: c})
		}
		// Build clients eagerly so failures are discovered once at startup time.
		for _, k := range r.cfg.APIKeys {
			add(k)
		}
		r.logf("[GEMINI] initialized clients=%d", len(r.clients))
	})
}

// HasConfigured reports whether at least one usable Gemini client was created.
func (r *Runtime) HasConfigured() bool {
	r.loadKeys()
	return len(r.clients) > 0
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

	prompt := ""
	if strings.TrimSpace(description) != "" {
		prompt += "[Reference Context]\n\"\"\"\n" + strings.TrimSpace(description) + "\n\"\"\"\n\n"
	}
	prompt += "[Original]\n\"\"\"\n" + normalizeRefineInputText(rawText) + "\n\"\"\"\n"

	// Retry across the client pool while honoring per-key cooldown windows.
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

		text, err := r.generate(idx, prompt)
		if err == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), nil
		}
		lastErr = err
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

// nextReadyClient returns the next client whose cooldown window has expired.
func (r *Runtime) nextReadyClient(now time.Time) (int, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.clients) == 0 {
		return -1, 0
	}

	start := r.index
	var minWait time.Duration
	for i := 0; i < len(r.clients); i++ {
		idx := (start + i) % len(r.clients)
		wait := r.clients[idx].cooldownUntil.Sub(now)
		if wait <= 0 {
			r.index = (idx + 1) % len(r.clients)
			return idx, 0
		}
		if minWait == 0 || wait < minWait {
			minWait = wait
		}
	}
	return -1, minWait
}

// generate performs one refine request using the selected Gemini client.
func (r *Runtime) generate(idx int, prompt string) (string, error) {
	r.mu.Lock()
	if idx < 0 || idx >= len(r.clients) {
		r.mu.Unlock()
		return "", errors.New("invalid client index")
	}
	c := r.clients[idx].client
	keySuffix := maskedKeySuffix(r.clients[idx].key)
	r.mu.Unlock()

	// Refinement requests use structured JSON output so later stages can render reliably.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	responseSchema, err := parseRefineResponseSchema()
	if err != nil {
		r.onFailure(idx, err)
		return "", err
	}
	result, err := c.Models.GenerateContent(
		ctx,
		r.cfg.Model,
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

const refineSystemPrompt = `# Role
You are a professional 'Speech-to-Text (STT) Correction Editor.' You possess an exceptional ability to grasp the context of fragmented transcription data and refine incomplete sentences into natural, accurate spoken scripts-not formal written text. Your highest priority is to preserve every detail of the original speech without omitting any content.

# Task
The provided [Original] text is a result of transcribing lectures or speeches using an STT engine. It contains typos, spacing errors, grammatical mistakes, and fragmented sentences. Refine this text according to the [Guidelines] below, ensuring **Zero Omission**.

# Guidelines
1. **Contextual Correction:**
   - **Correct mis-transcribed words** that sound similar based on the context. (e.g., '정보의미' -> '정보 은닉', '이네이턴스' -> 'Inheritance(상속)')
   - Ensure technical terms use accurate notation. Format code variables or operators according to programming syntax. (e.g., '데이터 스트럭처' -> 'Data Structure(자료구조)', 'M 퍼센트' -> '&')

2. **No Omission:**
   - **Never summarize the content or shorten sentences.** Be vigilant against the tendency to merge or condense sentences toward the end of the text.
   - Do not arbitrarily delete any part of the original speech, including the speaker's intent, small talk, additional explanations, or exclamations.
   - Every spoken element must be included in the output. (Meaningless repetitive stammers or filler sounds may be cleaned up naturally.)
   - Do not change the original meaning or distort facts during the refining process.
   - The volume of the output text must be nearly identical to the volume of the original text.

3. **Complete Sentence Construction:**
   - Transform lists of fragmented words into grammatically correct sentences. Use commas (,) and periods (.) appropriately to enhance readability.

4. **Contextual Paragraphing:**
   - Group sentences that discuss a single topic into a paragraph.
   - This means creating a paragraph that contains the refined sentences, not merging them into one long sentence. All sentences within a paragraph must be output as refined.
   - Start a new paragraph when the topic shifts or the flow of conversation changes.

5. **Timeline Integrity:**
   - Never arbitrarily modify or omit the timestamps (start_time) assigned to each sentence in the [Original] data.
   - Maintain precise timeline mapping for each refined sentence, even when grouping them into paragraphs.

# Output Format
{ "paragraph": [ { "paragraph_summary": "문단 요약 정리", "sentence": [ { "start_time": "[00:00:00,000]", "content": "문장 정제 내용1" } ] } ] }`

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

// parseRefineResponseSchema loads the JSON schema used for transcript refinement.
func parseRefineResponseSchema() (*genai.Schema, error) {
	var schema genai.Schema
	if err := json.Unmarshal([]byte(refineResponseSchemaJSON), &schema); err != nil {
		return nil, err
	}
	return &schema, nil
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

// normalizeRefineInputText converts legacy timestamp lines into the current prompt format.
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

func (r *Runtime) onSuccess(idx int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if idx < 0 || idx >= len(r.clients) {
		return
	}
	r.clients[idx].failCount = 0
	r.clients[idx].cooldownUntil = time.Time{}
}

func (r *Runtime) onFailure(idx int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if idx < 0 || idx >= len(r.clients) {
		return
	}

	base := 2 * time.Second
	c := &r.clients[idx]
	c.failCount++
	backoff := base * time.Duration(1<<(min(c.failCount-1, 4)))
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	if !isRetryableGeminiError(err) {
		backoff = 5 * time.Second
	}
	c.cooldownUntil = time.Now().Add(backoff)
	r.errf("gemini.generate", err, "api_key_suffix=%s cooldown=%s fail_count=%d", maskedKeySuffix(c.key), backoff, c.failCount)
}

func (r *Runtime) ExtractDocumentChunk(ctx context.Context, chunk worker.DocumentChunk, consistencyContext string) ([]byte, error) {
	r.loadKeys()
	r.mu.Lock()
	clientCount := len(r.clients)
	r.mu.Unlock()
	if clientCount == 0 {
		return nil, errors.New("gemini api is not configured")
	}

	systemPrompt, err := r.documentSystemPrompt()
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
		result, genErr := r.generateDocument(ctx, idx, systemPrompt, schema, instructionText, chunk)
		if genErr == nil && len(result) > 0 {
			return result, nil
		}
		lastErr = genErr
	}
	return nil, lastErr
}

func (r *Runtime) generateDocument(ctx context.Context, idx int, systemPrompt string, schema *genai.Schema, instruction string, chunk worker.DocumentChunk) ([]byte, error) {
	r.mu.Lock()
	if idx < 0 || idx >= len(r.clients) {
		r.mu.Unlock()
		return nil, errors.New("invalid client index")
	}
	c := r.clients[idx].client
	keySuffix := maskedKeySuffix(r.clients[idx].key)
	r.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(r.cfg.PDFBatchTimeoutSec)*time.Second)
	defer cancel()

	parts := []*genai.Part{genai.NewPartFromText(instruction)}
	for _, image := range chunk.Images {
		parts = append(parts, genai.NewPartFromText(fmt.Sprintf("Page %d", image.PageIndex)))
		parts = append(parts, genai.NewPartFromBytes(image.Data, image.MIMEType))
	}

	result, err := c.Models.GenerateContent(
		callCtx,
		r.cfg.Model,
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
		r.onFailure(idx, err)
		return nil, err
	}
	if result == nil || strings.TrimSpace(result.Text()) == "" {
		err = errors.New("empty response text")
		r.onFailure(idx, err)
		return nil, err
	}
	normalized, err := normalizeDocumentResponseJSON(result.Text())
	if err != nil {
		r.onFailure(idx, err)
		return nil, err
	}
	normalized, err = alignDocumentPageIndexes(normalized, chunk)
	if err != nil {
		r.onFailure(idx, err)
		return nil, err
	}
	r.onSuccess(idx)
	r.logf("[GEMINI] document success api_key_suffix=%s chunk=%d/%d", keySuffix, chunk.ChunkIndex, chunk.TotalChunks)
	return normalized, nil
}

func (r *Runtime) documentSystemPrompt() (string, error) {
	r.documentPromptOnce.Do(func() {
		promptPath := r.cfg.PromptPath
		if strings.TrimSpace(promptPath) == "" {
			promptPath = filepath.Join("docs", "prompts", "file_transcript_system_prompt.md")
		}
		b, err := os.ReadFile(promptPath)
		if err != nil {
			r.documentPromptErr = err
			return
		}
		r.documentPromptText = strings.TrimSpace(string(b)) + "\n\nAdditional rules:\n- Treat each image as a PDF page.\n- The request may contain only a subset of the document pages.\n- Preserve terminology and heading structure consistently across batches.\n- Return only the pages included in the current batch."
	})
	return r.documentPromptText, r.documentPromptErr
}

func (r *Runtime) BuildConsistencyContext(raw []byte) (string, error) {
	return buildConsistencyContext(raw, r.cfg.PDFConsistencyContextMaxChars)
}

func (r *Runtime) MergeDocumentJSON(blobs ...[]byte) ([]byte, error) {
	return mergeDocumentJSON(blobs...)
}

func (r *Runtime) RenderDocumentMarkdown(raw []byte) (string, error) {
	return renderDocumentMarkdown(raw)
}

// isRetryableGeminiError classifies transient Gemini failures worth retrying.
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

// maskedKeySuffix returns a safe-to-log suffix for an API key.
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

// min returns the smaller of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
