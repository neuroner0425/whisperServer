// types.go contains small server-local DTOs used while shaping transport responses.
package server

// JobView is the server-local detail payload shaped before transport serialization.
type JobView struct {
	Filename           string
	FileType           string
	Status             string
	UploadedAt         string
	StartedAt          string
	CompletedAt        string
	Duration           string
	MediaDuration      string
	Phase              string
	ProgressLabel      string
	ProgressPercent    int
	PreviewText        string
	StatusDetail       string
	PageCount          int
	ProcessedPageCount int
	CurrentChunk       int
	TotalChunks        int
	ResumeAvailable    bool
}
