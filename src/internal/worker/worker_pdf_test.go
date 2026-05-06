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
	chunks := [][]string{
		{"p1", "p2", "p3", "p4", "p5"},
		{"p6", "p7", "p8", "p9"},
		{"p10"},
	}
	if got := processedPagesForChunk(2, chunks); got != 9 {
		t.Fatalf("unexpected processed pages: %d", got)
	}
	if got := processedPagesForChunk(5, chunks); got != 10 {
		t.Fatalf("processed pages should cap at page count: %d", got)
	}
}

func TestSplitPagePathsBalancesChunksWithinLimit(t *testing.T) {
	paths90 := make([]string, 90)
	for i := range paths90 {
		paths90[i] = "p"
	}
	chunks90 := splitPagePaths(paths90, 50)
	if len(chunks90) != 2 || len(chunks90[0]) != 45 || len(chunks90[1]) != 45 {
		t.Fatalf("expected 90 pages to split into 45/45, got %d/%d", len(chunks90[0]), len(chunks90[1]))
	}

	paths105 := make([]string, 105)
	for i := range paths105 {
		paths105[i] = "p"
	}
	chunks105 := splitPagePaths(paths105, 50)
	if len(chunks105) != 3 || len(chunks105[0]) != 35 || len(chunks105[1]) != 35 || len(chunks105[2]) != 35 {
		t.Fatalf("expected 105 pages to split into 35/35/35, got %d/%d/%d", len(chunks105[0]), len(chunks105[1]), len(chunks105[2]))
	}

	paths101 := make([]string, 101)
	for i := range paths101 {
		paths101[i] = "p"
	}
	chunks101 := splitPagePaths(paths101, 50)
	if len(chunks101) != 3 || len(chunks101[0]) > 50 || len(chunks101[1]) > 50 || len(chunks101[2]) > 50 {
		t.Fatalf("expected all chunk sizes to stay <= 50, got %d/%d/%d", len(chunks101[0]), len(chunks101[1]), len(chunks101[2]))
	}
}
