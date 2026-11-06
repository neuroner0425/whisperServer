package domain

// DocumentPageImage is one rendered PDF page passed to document extraction.
type DocumentPageImage struct {
	PageIndex int
	MIMEType  string
	Data      []byte
}

// DocumentChunk is one batch of rendered PDF pages sent for extraction.
type DocumentChunk struct {
	ChunkIndex  int
	TotalChunks int
	StartPage   int
	EndPage     int
	TotalPages  int
	Images      []DocumentPageImage
}

