package app

import (
	"regexp"
	"strings"
	"time"

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

func cancelJob(jobID string) {
	if appWorker != nil {
		appWorker.Cancel(jobID)
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
	owners := map[string]struct{}{}
	for _, id := range ids {
		cancelJob(id)
		if job := runtimeState.jobs[id]; job != nil && job.OwnerID != "" {
			owners[job.OwnerID] = struct{}{}
		}
		removeTempWav(id)
		store.DeleteJobBlobs(id)
		delete(runtimeState.jobs, id)
	}
	saveJobsLocked()
	for ownerID := range owners {
		eventBroker.Notify(ownerID, "files.changed", nil)
	}
}

func loadJobs() {
	loaded, err := store.LoadJobs()
	if err != nil {
		procErrf("storage.loadJobs", err, "load from db failed")
		return
	}
	for _, job := range loaded {
		hydrateJobDerivedFields(job)
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
	eventBroker.Notify(job.OwnerID, "files.changed", map[string]any{"job_id": id})
}

func applyJobFields(job *model.Job, fields map[string]any) {
	for k, v := range fields {
		switch k {
		case "status":
			job.Status = intutil.AsString(v)
			job.StatusCode = model.JobStatusCode(job.Status)
		case "status_code":
			job.StatusCode = intutil.AsInt(v)
		case "filename":
			job.Filename = intutil.AsString(v)
		case "file_type":
			job.FileType = intutil.AsString(v)
		case "result":
			job.Result = intutil.AsString(v)
		case "uploaded_at":
			job.UploadedTS = parseJobTimestamp(intutil.AsString(v))
		case "uploaded_ts":
			job.UploadedTS = intutil.AsFloat(v)
		case "duration":
			// duration is derived from started_ts/completed_ts.
		case "media_duration":
			// media duration is derived from media_duration_seconds.
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
			job.DeletedTS = parseJobTimestamp(intutil.AsString(v))
		case "deleted_ts":
			job.DeletedTS = intutil.AsFloat(v)
		case "started_at":
			job.StartedTS = parseJobTimestamp(intutil.AsString(v))
		case "started_ts":
			job.StartedTS = intutil.AsFloat(v)
		case "completed_at":
			job.CompletedTS = parseJobTimestamp(intutil.AsString(v))
		case "completed_ts":
			job.CompletedTS = intutil.AsFloat(v)
		case "phase":
			job.Phase = intutil.AsString(v)
		case "progress_percent":
			job.ProgressPercent = intutil.AsInt(v)
		case "progress_label":
			// progress label is derived from phase/status.
		case "preview_text":
			job.PreviewText = intutil.AsString(v)
		case "result_refined":
			job.ResultRefined = intutil.AsString(v)
		case "status_detail":
			job.StatusDetail = intutil.AsString(v)
		}
	}
	hydrateJobDerivedFields(job)
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
		eventBroker.Notify(ownerID, "files.changed", nil)
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
	if err := store.SaveJobBlob(id, store.BlobKindPreview, []byte(prev)); err != nil {
		procErrf("storage.savePreviewBlob", err, "job_id=%s", id)
	}
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

func hydrateJobDerivedFields(job *model.Job) {
	if job == nil {
		return
	}
	if job.StatusCode == 0 {
		job.StatusCode = model.JobStatusCode(job.Status)
	}
	job.Status = model.JobStatusName(job.StatusCode)
	job.UploadedAt = formatJobTimestamp(job.UploadedTS)
	job.DeletedAt = formatJobTimestamp(job.DeletedTS)
	job.StartedAt = formatJobTimestamp(job.StartedTS)
	job.CompletedAt = formatJobTimestamp(job.CompletedTS)
	job.MediaDuration = formatDurationSeconds(job.MediaDurationSeconds)
	job.Duration = deriveJobDuration(job.StartedTS, job.CompletedTS)
	if strings.TrimSpace(job.Phase) == "" {
		job.Phase = deriveJobPhase(job.StatusCode)
	}
	job.ProgressLabel = deriveJobProgressLabel(job)
}

func parseJobTimestamp(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if ts, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return float64(ts.Unix())
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return float64(ts.Unix())
	}
	return 0
}

func formatJobTimestamp(ts float64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(int64(ts), 0).Format("2006-01-02 15:04:05")
}

func formatDurationSeconds(sec *int) string {
	if sec == nil {
		return ""
	}
	return intutil.FormatSeconds(*sec)
}

func deriveJobDuration(startedTS, completedTS float64) string {
	if startedTS <= 0 || completedTS <= 0 || completedTS < startedTS {
		return ""
	}
	return intutil.FormatSeconds(int(completedTS - startedTS))
}

func deriveJobProgressLabel(job *model.Job) string {
	if job == nil {
		return ""
	}
	if strings.TrimSpace(job.Phase) != "" {
		return job.Phase
	}
	return job.Status
}

func deriveJobPhase(statusCode int) string {
	switch statusCode {
	case model.JobStatusRunningCode:
		return "전사 중"
	case model.JobStatusRefiningPendingCode:
		return "정제 대기 중"
	case model.JobStatusRefiningCode:
		return "정제 중"
	case model.JobStatusCompletedCode:
		return "완료"
	case model.JobStatusFailedCode:
		return "실패"
	default:
		return "대기 중"
	}
}

func toJobView(job *model.Job) JobView {
	return JobView{
		Filename:        job.Filename,
		FileType:        job.FileType,
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
