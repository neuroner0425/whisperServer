package worker

import "testing"

func TestHasContinuousChunkJSON(t *testing.T) {
	kinds := map[string]struct{}{
		pdfChunkJSONKind(1): {},
		pdfChunkJSONKind(2): {},
		pdfChunkJSONKind(3): {},
	}
	if !hasContinuousChunkJSON(kinds, 3) {
		t.Fatalf("expected continuous chunk sequence")
	}
	delete(kinds, pdfChunkJSONKind(2))
	if hasContinuousChunkJSON(kinds, 3) {
		t.Fatalf("expected missing chunk to break continuity")
	}
}

func TestProcessedPagesForChunk(t *testing.T) {
	if got := processedPagesForChunk(2, 250, 80); got != 160 {
		t.Fatalf("unexpected processed pages: %d", got)
	}
	if got := processedPagesForChunk(5, 250, 80); got != 250 {
		t.Fatalf("processed pages should cap at page count: %d", got)
	}
}
