package service

import model "whisperserver/src/internal/domain"

// ResumeRestoredJob best-effort resumes work for a restored job based on which blobs exist.
func ResumeRestoredJob(
	jobID string,
	job *model.Job,
	blob *JobBlobService,
	setJobFields func(string, map[string]any),
	enqueueTranscribe func(string),
	enqueueRefine func(string),
	enqueuePDFExtract func(string),
	statusPending string,
	statusRefiningPending string,
) {
	if job == nil || blob == nil || setJobFields == nil {
		return
	}

	if job.FileType == "pdf" && blob.HasPDFOriginal(jobID) && !blob.HasDocumentMarkdown(jobID) {
		setJobFields(jobID, map[string]any{
			"status":           statusPending,
			"phase":            "",
			"progress_percent": 0,
			"progress_label":   "",
			"started_at":       "",
			"started_ts":       0,
			"completed_at":     "",
			"completed_ts":     0,
			"duration":         "",
			"status_detail":    "",
		})
		if enqueuePDFExtract != nil {
			enqueuePDFExtract(jobID)
		}
		return
	}

	if blob.HasAudioAAC(jobID) && !blob.HasTranscript(jobID) {
		setJobFields(jobID, map[string]any{
			"status":           statusPending,
			"phase":            "",
			"progress_percent": 0,
			"progress_label":   "",
			"started_at":       "",
			"started_ts":       0,
			"completed_at":     "",
			"completed_ts":     0,
			"duration":         "",
			"status_detail":    "",
		})
		if enqueueTranscribe != nil {
			enqueueTranscribe(jobID)
		}
		return
	}

	if blob.HasTranscript(jobID) && !blob.HasRefined(jobID) && job.RefineEnabled {
		setJobFields(jobID, map[string]any{
			"status":         statusRefiningPending,
			"progress_label": "",
			"completed_at":   "",
			"completed_ts":   0,
			"duration":       "",
			"status_detail":  "",
		})
		if enqueueRefine != nil {
			enqueueRefine(jobID)
		}
	}
}
