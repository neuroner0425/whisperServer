package gemini

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"whisperserver/src/internal/worker"

	"google.golang.org/genai"
)

// Config defines Gemini-specific runtime settings and callbacks.
type Config struct {
	Model                          string
	APIKeys                        []string
	PDFBatchTimeoutSec             int
	PDFConsistencyContextMaxChars  int
	TranscriptSystemPromptPath     string
	TranscriptResponseSchemaPath   string
	RefineTimelineSystemPromptPath string
	DocumentSystemPromptPath       string
	DocumentResponseSchemaPath     string
	Logf                           func(format string, args ...any)
	Errf                           func(scope string, err error, format string, args ...any)
}

// Runtime owns Gemini clients, key rotation, and request helpers.
type Runtime struct {
	cfg Config
	mu  sync.Mutex

	clients []geminiKeyClient
	index   int
	load    sync.Once

	globalCooldownUntil time.Time

	transcriptPromptOnce sync.Once
	transcriptPromptText string
	transcriptPromptErr  error
	transcriptSchemaOnce sync.Once
	transcriptSchema     *genai.Schema
	transcriptSchemaErr  error

	refineTimelinePromptOnce sync.Once
	refineTimelinePromptText string
	refineTimelinePromptErr  error

	documentPromptOnce sync.Once
	documentPromptText string
	documentPromptErr  error
	documentSchemaOnce sync.Once
	documentSchema     *genai.Schema
	documentSchemaErr  error
}

// geminiKeyClient tracks one API key together with cooldown state.
type geminiKeyClient struct {
	key           string
	client        *genai.Client
	failCount     int
	cooldownUntil time.Time
}

var legacyTimelineLineRe = regexp.MustCompile(`^\[(\d{2}:\d{2}:\d{2})\.(\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2})\.(\d{3})\]\s*(.*)$`)

const (
	geminiClientInitTimeout = 60 * time.Second
	minGeminiRetryInterval  = 10 * time.Second
)

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
			ctx, cancel := context.WithTimeout(context.Background(), geminiClientInitTimeout)
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

// nextReadyClient returns the next client whose cooldown window has expired.
func (r *Runtime) nextReadyClient(now time.Time) (int, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.clients) == 0 {
		return -1, 0
	}
	if wait := r.globalCooldownUntil.Sub(now); wait > 0 {
		return -1, wait
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

func (r *Runtime) waitForReadyClient(ctx context.Context) (int, error) {
	for {
		idx, waitFor := r.nextReadyClient(time.Now())
		if idx >= 0 {
			return idx, nil
		}
		if waitFor <= 0 {
			waitFor = minGeminiRetryInterval
		}
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return -1, ctx.Err()
		case <-timer.C:
		}
	}
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
	now := time.Now()
	c.cooldownUntil = now.Add(backoff)
	globalBackoff := minGeminiRetryInterval
	if globalBackoff > backoff {
		backoff = globalBackoff
	}
	if until := now.Add(globalBackoff); until.After(r.globalCooldownUntil) {
		r.globalCooldownUntil = until
	}
	r.errf("gemini.generate", err, "api_key_suffix=%s cooldown=%s fail_count=%d", maskedKeySuffix(c.key), backoff, c.failCount)
}

func emptyResponseError(result *genai.GenerateContentResponse) error {
	return fmt.Errorf("empty response text (%s)", summarizeGenerateContentResponse(result))
}

func summarizeGenerateContentResponse(result *genai.GenerateContentResponse) string {
	if result == nil {
		return "nil_result"
	}

	parts := []string{
		"candidates=" + strconv.Itoa(len(result.Candidates)),
	}
	if strings.TrimSpace(result.ModelVersion) != "" {
		parts = append(parts, "model_version="+strings.TrimSpace(result.ModelVersion))
	}
	if strings.TrimSpace(result.ResponseID) != "" {
		parts = append(parts, "response_id="+strings.TrimSpace(result.ResponseID))
	}
	if result.UsageMetadata != nil {
		parts = append(parts,
			fmt.Sprintf("tokens[prompt=%d,candidates=%d,thoughts=%d,total=%d]",
				result.UsageMetadata.PromptTokenCount,
				result.UsageMetadata.CandidatesTokenCount,
				result.UsageMetadata.ThoughtsTokenCount,
				result.UsageMetadata.TotalTokenCount,
			),
		)
	}
	if result.PromptFeedback != nil {
		promptBits := []string{}
		if result.PromptFeedback.BlockReason != "" {
			promptBits = append(promptBits, "block_reason="+string(result.PromptFeedback.BlockReason))
		}
		if strings.TrimSpace(result.PromptFeedback.BlockReasonMessage) != "" {
			promptBits = append(promptBits, "block_message="+strings.TrimSpace(result.PromptFeedback.BlockReasonMessage))
		}
		if len(result.PromptFeedback.SafetyRatings) > 0 {
			promptBits = append(promptBits, "safety="+summarizeSafetyRatings(result.PromptFeedback.SafetyRatings))
		}
		if len(promptBits) > 0 {
			parts = append(parts, "prompt_feedback{"+strings.Join(promptBits, ",")+"}")
		}
	}
	if len(result.Candidates) > 0 && result.Candidates[0] != nil {
		cand := result.Candidates[0]
		candBits := []string{
			"finish_reason=" + string(cand.FinishReason),
			"token_count=" + strconv.Itoa(int(cand.TokenCount)),
		}
		if strings.TrimSpace(cand.FinishMessage) != "" {
			candBits = append(candBits, "finish_message="+strings.TrimSpace(cand.FinishMessage))
		}
		if cand.Content == nil {
			candBits = append(candBits, "content=nil")
		} else {
			candBits = append(candBits, "parts="+strconv.Itoa(len(cand.Content.Parts)))
			textParts := 0
			nonEmptyTextParts := 0
			for _, part := range cand.Content.Parts {
				if part == nil {
					continue
				}
				if part.Text != "" {
					textParts++
					if strings.TrimSpace(part.Text) != "" {
						nonEmptyTextParts++
					}
				}
			}
			candBits = append(candBits,
				"text_parts="+strconv.Itoa(textParts),
				"non_empty_text_parts="+strconv.Itoa(nonEmptyTextParts),
			)
		}
		if len(cand.SafetyRatings) > 0 {
			candBits = append(candBits, "safety="+summarizeSafetyRatings(cand.SafetyRatings))
		}
		parts = append(parts, "candidate0{"+strings.Join(candBits, ",")+"}")
	}
	return strings.Join(parts, " ")
}

func summarizeSafetyRatings(ratings []*genai.SafetyRating) string {
	if len(ratings) == 0 {
		return ""
	}
	out := make([]string, 0, len(ratings))
	for _, rating := range ratings {
		if rating == nil {
			continue
		}
		item := string(rating.Category) + ":" + string(rating.Probability)
		if rating.Blocked {
			item += ":blocked"
		}
		out = append(out, item)
	}
	return strings.Join(out, "|")
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
	schema, err := r.documentResponseSchema()
	if err != nil {
		return nil, err
	}

	instructionText := buildDocumentChunkPrompt(chunk, consistencyContext)
	var lastErr error = errors.New("gemini document request failed")
	maxAttempts := clientCount * 3
	for attempt := 0; attempt < maxAttempts; {
		idx, waitErr := r.waitForReadyClient(ctx)
		if waitErr != nil {
			return nil, waitErr
		}
		attempt++
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
			Temperature: genai.Ptr[float32](0.9),
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
		err = emptyResponseError(result)
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

func (r *Runtime) BuildConsistencyContext(raw []byte) (string, error) {
	return buildConsistencyContext(raw, r.cfg.PDFConsistencyContextMaxChars)
}

func (r *Runtime) MergeDocumentJSON(blobs ...[]byte) ([]byte, error) {
	return mergeDocumentJSON(blobs...)
}

func (r *Runtime) RenderDocumentMarkdown(raw []byte) (string, error) {
	return RenderDocumentMarkdown(raw)
}

func RenderDocumentMarkdown(raw []byte) (string, error) {
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
