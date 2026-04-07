package sqlite

import "database/sql"

// Blob kind constants define the persisted binary payloads attached to a job.
const (
	BlobKindAudioAAC           = "audio_aac"
	BlobKindWav                = "wav"
	BlobKindPDFOriginal        = "pdf_original"
	BlobKindPreview            = "preview"
	BlobKindTranscript         = "transcript"
	BlobKindTranscriptJSON     = "transcript_json"
	BlobKindRefined            = "refined"
	BlobKindDocumentJSON       = "document_json"
	BlobKindDocumentMarkdown   = "document_markdown"
	BlobKindDocumentChunkIndex = "document_chunk_index"
)

var (
	dbConn              *sql.DB
	logf                = func(string, ...any) {}
	errorf              = func(string, error, string, ...any) {}
	runtimeArtifactsDir string
)

const dbMaintenanceVersion = 1

// ConfigureLogging wires repository logging callbacks used by all SQLite helpers.
func ConfigureLogging(info func(string, ...any), err func(string, error, string, ...any)) {
	if info != nil {
		logf = info
	}
	if err != nil {
		errorf = err
	}
}

// Close releases the shared SQLite connection.
func Close() {
	if dbConn != nil {
		_ = dbConn.Close()
		dbConn = nil
	}
}
