package gemini

import (
	"encoding/json"
	"fmt"
	"strings"

	"whisperserver/src/internal/structured"
	"whisperserver/src/internal/worker"
)

// buildDocumentChunkPrompt describes the current PDF chunk and prior consistency hints.
func buildDocumentChunkPrompt(chunk worker.DocumentChunk, consistencyContext string) string {
	lines := []string{
		"[Document Batch Info]",
		fmt.Sprintf("- Total pages: %d", chunk.TotalPages),
		fmt.Sprintf("- Current chunk: %d/%d", chunk.ChunkIndex, chunk.TotalChunks),
		fmt.Sprintf("- Current page range: %d-%d", chunk.StartPage, chunk.EndPage),
		"",
		"[Instructions]",
		"- This request may contain only part of the full document.",
		"- Return only the pages included in this batch.",
		"- Keep terminology and heading hierarchy consistent with previous chunks when possible.",
		"- If the current page evidence conflicts with previous context, trust the current page.",
	}
	if strings.TrimSpace(consistencyContext) != "" {
		lines = append(lines,
			"",
			"[Consistency Context From Previous Chunks]",
			strings.TrimSpace(consistencyContext),
		)
	}
	return strings.Join(lines, "\n")
}

// normalizeDocumentResponseJSON trims Gemini output into a stable pretty-printed form.
func normalizeDocumentResponseJSON(raw string) ([]byte, error) {
	return structured.NormalizeDocumentResponseJSON(raw)
}

// alignDocumentPageIndexes rewrites page indexes to match the source chunk metadata.
func alignDocumentPageIndexes(raw []byte, chunk worker.DocumentChunk) ([]byte, error) {
	var parsed structured.DocumentResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	for i := range parsed.Pages {
		if i < len(chunk.Images) {
			parsed.Pages[i].PageIndex = chunk.Images[i].PageIndex
		}
	}
	return json.MarshalIndent(parsed, "", "  ")
}

// buildConsistencyContext extracts stable headings and terms from previous chunks.
func buildConsistencyContext(raw []byte, maxChars int) (string, error) {
	return structured.BuildConsistencyContext(raw, maxChars)
}

// mergeDocumentJSON concatenates chunk-level document JSON into one response body.
func mergeDocumentJSON(blobs ...[]byte) ([]byte, error) {
	return structured.MergeDocumentJSON(blobs...)
}

// renderDocumentMarkdown converts structured document JSON into markdown output.
func renderDocumentMarkdown(raw []byte) (string, error) {
	return structured.RenderDocumentMarkdown(raw)
}
