package service

import (
	"testing"

	model "whisperserver/src/internal/domain"
)

func TestResumeRestoredJob_PDFQueuesExtract(t *testing.T) {
	var (
		setCalled   bool
		queuedPDF   bool
		queuedAudio bool
		queuedRef   bool
	)

	blob := NewJobBlobService(JobBlobServiceDeps{
		HasJobBlob: func(_ string, kind string) bool {
			switch kind {
			case "pdf":
				return true
			case "doc":
				return false
			default:
				return false
			}
		},
		BlobKindPDFOriginal:      "pdf",
		BlobKindDocumentMarkdown: "doc",
	})

	ResumeRestoredJob(
		"j1",
		&model.Job{FileType: "pdf"},
		blob,
		func(string, map[string]any) { setCalled = true },
		func(string) { queuedAudio = true },
		func(string) { queuedRef = true },
		func(string) { queuedPDF = true },
		"pending",
		"refining_pending",
	)

	if !setCalled || !queuedPDF {
		t.Fatalf("expected setCalled=%v queuedPDF=%v", setCalled, queuedPDF)
	}
	if queuedAudio || queuedRef {
		t.Fatalf("unexpected queuedAudio=%v queuedRef=%v", queuedAudio, queuedRef)
	}
}

func TestResumeRestoredJob_AudioQueuesTranscribe(t *testing.T) {
	var queued bool
	blob := NewJobBlobService(JobBlobServiceDeps{
		HasJobBlob: func(_ string, kind string) bool {
			switch kind {
			case "aac":
				return true
			case "tr":
				return false
			default:
				return false
			}
		},
		BlobKindAudioAAC:   "aac",
		BlobKindTranscript: "tr",
	})
	ResumeRestoredJob(
		"j1",
		&model.Job{FileType: "audio"},
		blob,
		func(string, map[string]any) {},
		func(string) { queued = true },
		nil,
		nil,
		"pending",
		"refining_pending",
	)
	if !queued {
		t.Fatalf("expected transcribe to be queued")
	}
}

func TestResumeRestoredJob_TranscriptQueuesRefineWhenEnabled(t *testing.T) {
	var queued bool
	var status any
	blob := NewJobBlobService(JobBlobServiceDeps{
		HasJobBlob: func(_ string, kind string) bool {
			switch kind {
			case "tr":
				return true
			case "ref":
				return false
			default:
				return false
			}
		},
		BlobKindTranscript: "tr",
		BlobKindRefined:    "ref",
	})
	ResumeRestoredJob(
		"j1",
		&model.Job{FileType: "audio", RefineEnabled: true},
		blob,
		func(_ string, fields map[string]any) { status = fields["status"] },
		nil,
		func(string) { queued = true },
		nil,
		"pending",
		"refining_pending",
	)
	if !queued {
		t.Fatalf("expected refine to be queued")
	}
	if status != "refining_pending" {
		t.Fatalf("expected status refining_pending, got %v", status)
	}
}
