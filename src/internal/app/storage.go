package app

import (
	"regexp"
	"strings"

	"whisperserver/src/internal/model"
	"whisperserver/src/internal/store"
	intutil "whisperserver/src/internal/util"
)

var (
	previewTimelineRe = regexp.MustCompile(`^\s*\[\d{2}:\d{2}:\d{2}(?:\.\d+)?\s*-->\s*\d{2}:\d{2}:\d{2}(?:\.\d+)?\]\s*`)
	previewBracketRe  = regexp.MustCompile(`^\s*\[[^\]]+\]\s*`)
)

func sanitizePreviewLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || line == `""` || line == `''` {
		return ""
	}
	line = previewTimelineRe.ReplaceAllString(line, "")
	line = previewBracketRe.ReplaceAllString(line, "")
	line = strings.TrimSpace(line)
	if line == "" || line == `""` || line == `''` {
		return ""
	}
	return line
}

func sanitizePreviewText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if s := sanitizePreviewLine(line); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}

func enqueueTranscribe(jobID string) {
	if appWorker != nil {
		appWorker.EnqueueTranscribe(jobID)
	}
}

func enqueueRefine(jobID string) {
	if appWorker != nil {
		appWorker.EnqueueRefine(jobID)
	}
}

func setQueueLen() {
	queueLength.Set(queueLengthValue())
}

func queueLengthValue() float64 {
	return 0
}

func requeuePending() {
	if appWorker != nil {
		appWorker.RequeuePending(jobsSnapshot())
	}
}

func deleteJobs(ids []string) {
	runtimeState.jobsMu.Lock()
	defer runtimeState.jobsMu.Unlock()
	for _, id := range ids {
		store.DeleteJobBlobs(id)
		delete(runtimeState.jobs, id)
	}
	saveJobsLocked()
}

func loadJobs() {
	loaded, err := store.LoadJobs()
	if err != nil {
		procErrf("storage.loadJobs", err, "load from db failed")
		return
	}
	runtimeState.jobsMu.Lock()
	runtimeState.jobs = loaded
	runtimeState.jobsMu.Unlock()
}

func saveJobsLocked() {
	if err := store.SaveJobs(runtimeState.jobs); err != nil {
		procErrf("storage.saveJobs", err, "save to db failed")
	}
}

func getJob(id string) *model.Job {
	runtimeState.jobsMu.RLock()
	defer runtimeState.jobsMu.RUnlock()
	job := runtimeState.jobs[id]
	return job.Clone()
}

func setJobFields(id string, fields map[string]any) {
	runtimeState.jobsMu.Lock()
	defer runtimeState.jobsMu.Unlock()
	job := runtimeState.jobs[id]
	if job == nil {
		return
	}
	applyJobFields(job, fields)
	saveJobsLocked()
}

func applyJobFields(job *model.Job, fields map[string]any) {
	for k, v := range fields {
		switch k {
		case "status":
			job.Status = intutil.AsString(v)
		case "filename":
			job.Filename = intutil.AsString(v)
		case "result":
			job.Result = intutil.AsString(v)
		case "uploaded_at":
			job.UploadedAt = intutil.AsString(v)
		case "uploaded_ts":
			job.UploadedTS = intutil.AsFloat(v)
		case "duration":
			job.Duration = intutil.AsString(v)
		case "media_duration":
			job.MediaDuration = intutil.AsString(v)
		case "media_duration_seconds":
			job.MediaDurationSeconds = intutil.AsIntPtr(v)
		case "description":
			job.Description = intutil.AsString(v)
		case "refine_enabled":
			job.RefineEnabled = intutil.AsBool(v)
		case "owner_id":
			job.OwnerID = intutil.AsString(v)
		case "tags":
			job.Tags = intutil.AsStringSlice(v)
		case "folder_id":
			job.FolderID = intutil.AsString(v)
		case "is_trashed":
			job.IsTrashed = intutil.AsBool(v)
		case "deleted_at":
			job.DeletedAt = intutil.AsString(v)
		case "started_at":
			job.StartedAt = intutil.AsString(v)
		case "started_ts":
			job.StartedTS = intutil.AsFloat(v)
		case "completed_at":
			job.CompletedAt = intutil.AsString(v)
		case "completed_ts":
			job.CompletedTS = intutil.AsFloat(v)
		case "phase":
			job.Phase = intutil.AsString(v)
		case "progress_percent":
			job.ProgressPercent = intutil.AsInt(v)
		case "progress_label":
			job.ProgressLabel = intutil.AsString(v)
		case "preview_text":
			job.PreviewText = intutil.AsString(v)
		case "result_refined":
			job.ResultRefined = intutil.AsString(v)
		case "status_detail":
			job.StatusDetail = intutil.AsString(v)
		}
	}
}

func removeTagFromOwnerJobs(ownerID, tagName string) {
	runtimeState.jobsMu.Lock()
	defer runtimeState.jobsMu.Unlock()
	changed := false
	for _, job := range runtimeState.jobs {
		if job.OwnerID != ownerID {
			continue
		}
		tags := append([]string(nil), job.Tags...)
		if len(tags) == 0 {
			continue
		}
		next := make([]string, 0, len(tags))
		removed := false
		for _, t := range tags {
			if t == tagName {
				removed = true
				continue
			}
			next = append(next, t)
		}
		if removed {
			job.Tags = next
			changed = true
		}
	}
	if changed {
		saveJobsLocked()
	}
}

func appendJobPreviewLine(id, line string) {
	line = sanitizePreviewLine(line)
	if line == "" {
		return
	}
	const maxPreviewChars = 40000

	runtimeState.jobsMu.Lock()
	defer runtimeState.jobsMu.Unlock()
	job := runtimeState.jobs[id]
	if job == nil {
		return
	}

	prev := sanitizePreviewText(job.PreviewText)
	if prev == "" {
		prev = line
	} else {
		prev = prev + "\n" + line
	}

	if len(prev) > maxPreviewChars {
		prev = prev[len(prev)-maxPreviewChars:]
	}

	job.PreviewText = prev
	saveJobsLocked()
}

func uploadedTS(id string) float64 {
	runtimeState.jobsMu.RLock()
	defer runtimeState.jobsMu.RUnlock()
	job := runtimeState.jobs[id]
	if job == nil {
		return 0
	}
	return job.UploadedTS
}

func toJobView(job *model.Job) JobView {
	return JobView{
		Filename:        job.Filename,
		Status:          job.Status,
		UploadedAt:      intutil.Fallback(job.UploadedAt, "-"),
		StartedAt:       intutil.Fallback(job.StartedAt, "-"),
		CompletedAt:     intutil.Fallback(job.CompletedAt, "-"),
		Duration:        durationString(job.Duration),
		MediaDuration:   intutil.Fallback(job.MediaDuration, "-"),
		Phase:           intutil.Fallback(job.Phase, "대기 중"),
		ProgressLabel:   intutil.Fallback(job.ProgressLabel, ""),
		ProgressPercent: job.ProgressPercent,
		PreviewText:     sanitizePreviewText(job.PreviewText),
	}
}

func durationString(v any) string {
	if v == nil {
		return "-"
	}
	s := intutil.AsString(v)
	if strings.TrimSpace(s) == "" || s == "<nil>" {
		return "-"
	}
	return s
}
