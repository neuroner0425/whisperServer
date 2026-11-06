package gemini

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"whisperserver/src/internal/worker"

	"google.golang.org/genai"
)

// APILogEntry represents the structured record of a Gemini API call.
type APILogEntry struct {
	Timestamp             string                `json:"timestamp"`
	CallType              string                `json:"call_type"` // e.g. "refine", "document"
	APIKeySuffix          string                `json:"api_key_suffix"`
	Model                 string                `json:"model"`
	DurationMs            int64                 `json:"duration_ms"`
	Request               APIRequestLog         `json:"request"`
	Response              APIResponseLog        `json:"response"`
	FailureCriteriaResult FailureCriteriaResult `json:"failure_criteria_result"`
}

type APIRequestLog struct {
	SystemPrompt     string         `json:"system_prompt,omitempty"`
	UserPrompt       string         `json:"user_prompt,omitempty"`
	ResponseMIMEType string         `json:"response_mime_type,omitempty"`
	HasSchema        bool           `json:"has_schema"`
	ImagesMeta       []ImageMetaLog `json:"images_meta,omitempty"` // For multimodal PDF calls
	Extra            map[string]any `json:"extra,omitempty"`
}

type ImageMetaLog struct {
	PageIndex int    `json:"page_index"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int    `json:"size_bytes"`
}

type APIResponseLog struct {
	RawText          string `json:"raw_text,omitempty"`
	FinishReason     string `json:"finish_reason,omitempty"`
	BlockReason      string `json:"block_reason,omitempty"`
	PromptTokens     int32  `json:"prompt_tokens"`
	CandidatesTokens int32  `json:"candidates_tokens"`
	ThoughtsTokens   int32  `json:"thoughts_tokens"`
	TotalTokens      int32  `json:"total_tokens"`
	Error            string `json:"error,omitempty"`
}

type FailureCriteriaResult struct {
	Failed            bool   `json:"failed"`
	MatchedCriterion  string `json:"matched_criterion,omitempty"`
	ReasonDescription string `json:"reason_description,omitempty"`
	IsRetryable       bool   `json:"is_retryable"`
}

var (
	logDirInitOnce sync.Once
	apiLogDir      = "logs/api"
)

// SetAPILogDir allows overriding log directory path (useful for testing or config).
func SetAPILogDir(dir string) {
	apiLogDir = dir
}

func ensureAPILogDir() error {
	var err error
	logDirInitOnce.Do(func() {
		err = os.MkdirAll(apiLogDir, 0755)
	})
	if err != nil {
		logDirInitOnce = sync.Once{}
		return err
	}
	return os.MkdirAll(apiLogDir, 0755)
}

// RecordAPILog writes an APILogEntry as a pretty-printed JSON file to logs/api/.
func RecordAPILog(entry APILogEntry) error {
	if err := ensureAPILogDir(); err != nil {
		return fmt.Errorf("failed to create api log dir: %w", err)
	}

	status := "success"
	if entry.FailureCriteriaResult.Failed {
		status = "failure"
	}

	now := time.Now()
	fileName := fmt.Sprintf("%s_%s_%s_%d.json",
		now.Format("20060102_150405"),
		entry.CallType,
		status,
		now.UnixNano()%1000000,
	)
	filePath := filepath.Join(apiLogDir, fileName)

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal api log entry: %w", err)
	}

	return os.WriteFile(filePath, data, 0644)
}

// extractResponseLog extracts summary fields from genai.GenerateContentResponse and error.
func extractResponseLog(result *genai.GenerateContentResponse, err error) APIResponseLog {
	respLog := APIResponseLog{}
	if err != nil {
		respLog.Error = err.Error()
	}
	if result != nil {
		respLog.RawText = strings.TrimSpace(result.Text())
		if result.UsageMetadata != nil {
			respLog.PromptTokens = result.UsageMetadata.PromptTokenCount
			respLog.CandidatesTokens = result.UsageMetadata.CandidatesTokenCount
			respLog.ThoughtsTokens = result.UsageMetadata.ThoughtsTokenCount
			respLog.TotalTokens = result.UsageMetadata.TotalTokenCount
		}
		if result.PromptFeedback != nil && result.PromptFeedback.BlockReason != "" {
			respLog.BlockReason = string(result.PromptFeedback.BlockReason)
		}
		if len(result.Candidates) > 0 && result.Candidates[0] != nil {
			respLog.FinishReason = string(result.Candidates[0].FinishReason)
		}
	}
	return respLog
}

// buildChunkImagesMeta extracts metadata from DocumentChunk without storing raw binary bytes.
func buildChunkImagesMeta(chunk worker.DocumentChunk) []ImageMetaLog {
	meta := make([]ImageMetaLog, 0, len(chunk.Images))
	for _, img := range chunk.Images {
		meta = append(meta, ImageMetaLog{
			PageIndex: img.PageIndex,
			MIMEType:  img.MIMEType,
			SizeBytes: len(img.Data),
		})
	}
	return meta
}
