package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/genai"
)

func (r *Runtime) transcriptSystemPrompt() (string, error) {
	r.transcriptPromptOnce.Do(func() {
		r.transcriptPromptText, r.transcriptPromptErr = readPromptAsset(
			r.cfg.TranscriptSystemPromptPath,
			filepath.Join("docs", "prompts", "transcript_system_prompt.md"),
			"",
		)
	})
	return r.transcriptPromptText, r.transcriptPromptErr
}

func (r *Runtime) transcriptResponseSchema() (*genai.Schema, error) {
	r.transcriptSchemaOnce.Do(func() {
		r.transcriptSchema, r.transcriptSchemaErr = readSchemaAsset(
			r.cfg.TranscriptResponseSchemaPath,
			filepath.Join("docs", "prompts", "transcript_response.json"),
		)
	})
	return r.transcriptSchema, r.transcriptSchemaErr
}

func (r *Runtime) refineTimelineSystemPrompt() (string, error) {
	r.refineTimelinePromptOnce.Do(func() {
		r.refineTimelinePromptText, r.refineTimelinePromptErr = readPromptAsset(
			r.cfg.RefineTimelineSystemPromptPath,
			filepath.Join("docs", "prompts", "refine_timeline_system_prompt.md"),
			"",
		)
	})
	return r.refineTimelinePromptText, r.refineTimelinePromptErr
}

func (r *Runtime) documentSystemPrompt() (string, error) {
	r.documentPromptOnce.Do(func() {
		r.documentPromptText, r.documentPromptErr = readPromptAsset(
			r.cfg.DocumentSystemPromptPath,
			filepath.Join("docs", "prompts", "file_transcript_system_prompt.md"),
			"\n\nAdditional rules:\n- Treat each image as a PDF page.\n- The request may contain only a subset of the document pages.\n- Preserve terminology and heading structure consistently across batches.\n- Return only the pages included in the current batch.",
		)
	})
	return r.documentPromptText, r.documentPromptErr
}

func (r *Runtime) documentResponseSchema() (*genai.Schema, error) {
	r.documentSchemaOnce.Do(func() {
		r.documentSchema, r.documentSchemaErr = readSchemaAsset(
			r.cfg.DocumentResponseSchemaPath,
			filepath.Join("docs", "prompts", "file_transcript_response.json"),
		)
	})
	return r.documentSchema, r.documentSchemaErr
}

func readPromptAsset(path string, fallback string, suffix string) (string, error) {
	body, err := readPromptFile(path, fallback)
	if err != nil {
		return "", err
	}
	if suffix != "" {
		body += suffix
	}
	return body, nil
}

func readPromptFile(path string, fallback string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = fallback
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func readSchemaAsset(path string, fallback string) (*genai.Schema, error) {
	if strings.TrimSpace(path) == "" {
		path = fallback
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var schema genai.Schema
	if err := json.Unmarshal(b, &schema); err != nil {
		return nil, err
	}
	return &schema, nil
}
