package app

import (
	"regexp"
	"strings"
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

func enqueue(t task) {
	taskQueue <- t
	setQueueLen()
}

func enqueueTranscribe(jobID string) {
	t := task{jobID: jobID, kind: taskTypeTranscribe}
	if splitTaskQueues {
		transcribeQueue <- t
	} else {
		enqueue(t)
		return
	}
	setQueueLen()
}

func enqueueRefine(jobID string) {
	t := task{jobID: jobID, kind: taskTypeRefine}
	if splitTaskQueues {
		refineQueue <- t
	} else {
		enqueue(t)
		return
	}
	setQueueLen()
}

func setQueueLen() {
	if splitTaskQueues {
		queueLength.Set(float64(len(transcribeQueue) + len(refineQueue)))
		return
	}
	queueLength.Set(float64(len(taskQueue)))
}

func requeuePending() {
	jobsMu.RLock()
	defer jobsMu.RUnlock()
	for id, job := range jobs {
		if isJobTrashed(job) {
			continue
		}
		status := asString(job["status"])
		switch status {
		case statusPending, statusRunning:
			if hasJobBlob(id, blobKindWav) {
				enqueueTranscribe(id)
			}
		case statusRefiningPending, statusRefining:
			if hasJobBlob(id, blobKindTranscript) {
				enqueueRefine(id)
			}
		}
	}
}

func deleteJobs(ids []string) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	for _, id := range ids {
		deleteJobBlobs(id)
		delete(jobs, id)
	}
	saveJobsLocked()
}

func loadJobs() {
	loaded, err := loadJobsFromDB()
	if err != nil {
		procErrf("storage.loadJobs", err, "load from db failed")
		return
	}
	jobsMu.Lock()
	jobs = loaded
	jobsMu.Unlock()
}

func saveJobsLocked() {
	if err := saveJobsToDB(jobs); err != nil {
		procErrf("storage.saveJobs", err, "save to db failed")
	}
}

func getJob(id string) map[string]any {
	jobsMu.RLock()
	defer jobsMu.RUnlock()
	job := jobs[id]
	if job == nil {
		return nil
	}
	cpy := make(map[string]any, len(job))
	for k, v := range job {
		cpy[k] = v
	}
	return cpy
}

func setJobFields(id string, fields map[string]any) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	job := jobs[id]
	if job == nil {
		return
	}
	for k, v := range fields {
		job[k] = v
	}
	saveJobsLocked()
}

func removeTagFromOwnerJobs(ownerID, tagName string) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	changed := false
	for _, job := range jobs {
		if asString(job["owner_id"]) != ownerID {
			continue
		}
		tags := asStringSlice(job["tags"])
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
			job["tags"] = next
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

	jobsMu.Lock()
	defer jobsMu.Unlock()
	job := jobs[id]
	if job == nil {
		return
	}

	prev := sanitizePreviewText(asString(job["preview_text"]))
	if prev == "" {
		prev = line
	} else {
		prev = prev + "\n" + line
	}

	if len(prev) > maxPreviewChars {
		prev = prev[len(prev)-maxPreviewChars:]
	}

	job["preview_text"] = prev
	saveJobsLocked()
}

func uploadedTS(id string) float64 {
	jobsMu.RLock()
	defer jobsMu.RUnlock()
	job := jobs[id]
	if job == nil {
		return 0
	}
	return asFloat(job["uploaded_ts"])
}

func toJobView(job map[string]any) JobView {
	return JobView{
		Filename:        asString(job["filename"]),
		Status:          asString(job["status"]),
		UploadedAt:      fallback(asString(job["uploaded_at"]), "-"),
		StartedAt:       fallback(asString(job["started_at"]), "-"),
		CompletedAt:     fallback(asString(job["completed_at"]), "-"),
		Duration:        durationString(job["duration"]),
		MediaDuration:   fallback(asString(job["media_duration"]), "-"),
		Phase:           fallback(asString(job["phase"]), "대기 중"),
		ProgressPercent: asInt(job["progress_percent"]),
		PreviewText:     sanitizePreviewText(asString(job["preview_text"])),
	}
}

func durationString(v any) string {
	if v == nil {
		return "-"
	}
	s := asString(v)
	if strings.TrimSpace(s) == "" || s == "<nil>" {
		return "-"
	}
	return s
}
