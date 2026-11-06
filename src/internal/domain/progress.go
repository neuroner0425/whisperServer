package domain

// ProgressSnapshot captures transient, volatile progress values that should not be persisted in SQLite.
type ProgressSnapshot struct {
	StatusCode         int    `json:"status_code"`
	Phase              string `json:"phase,omitempty"`
	ProgressPercent    int    `json:"progress_percent"`
	ProgressLabel      string `json:"progress_label,omitempty"`
	PreviewText        string `json:"preview_text,omitempty"`
	PageCount          int    `json:"page_count,omitempty"`
	ProcessedPageCount int    `json:"processed_page_count,omitempty"`
	CurrentChunk       int    `json:"current_chunk,omitempty"`
	TotalChunks        int    `json:"total_chunks,omitempty"`
}

