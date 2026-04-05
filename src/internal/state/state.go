package state

import (
	"strings"
	"sync"
	"time"

	"whisperserver/src/internal/model"
	intutil "whisperserver/src/internal/util"
)

type Deps struct {
	Now func() time.Time

	LoadJobs       func() (map[string]*model.Job, error)
	SaveJobs       func(map[string]*model.Job) error
	DeleteJobBlobs func(string)
	SaveJobBlob    func(string, string, []byte) error

	Notify        func(userID, eventType string, payload map[string]any)
	CancelJob     func(string)
	RemoveTempWav func(string)
	Errf          func(scope string, err error, format string, args ...any)
}

// State owns the in-memory job snapshot and persists it via store deps.
// This is still process-local by design.
type State struct {
	mu   sync.RWMutex
	jobs map[string]*model.Job
	d    Deps
}

func New(d Deps) *State {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &State{jobs: map[string]*model.Job{}, d: d}
}

func (s *State) JobsSnapshot() map[string]*model.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*model.Job, len(s.jobs))
	for id, job := range s.jobs {
		out[id] = job.Clone()
	}
	return out
}

func (s *State) Load() {
	if s == nil || s.d.LoadJobs == nil {
		return
	}
	loaded, err := s.d.LoadJobs()
	if err != nil {
		if s.d.Errf != nil {
			s.d.Errf("state.loadJobs", err, "load from db failed")
		}
		return
	}
	for _, job := range loaded {
		hydrateJobDerivedFields(job)
	}
	s.mu.Lock()
	s.jobs = loaded
	s.mu.Unlock()
}

func (s *State) AddJob(id string, job *model.Job) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hydrateJobDerivedFields(job)
	s.jobs[id] = job
	s.saveLocked()
	if job != nil && s.d.Notify != nil {
		s.d.Notify(job.OwnerID, "files.changed", map[string]any{"job_id": id})
	}
}

func (s *State) DeleteJobs(ids []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	owners := map[string]struct{}{}
	for _, id := range ids {
		if s.d.CancelJob != nil {
			s.d.CancelJob(id)
		}
		if job := s.jobs[id]; job != nil && job.OwnerID != "" {
			owners[job.OwnerID] = struct{}{}
		}
		if s.d.RemoveTempWav != nil {
			s.d.RemoveTempWav(id)
		}
		if s.d.DeleteJobBlobs != nil {
			s.d.DeleteJobBlobs(id)
		}
		delete(s.jobs, id)
	}
	s.saveLocked()
	for ownerID := range owners {
		if s.d.Notify != nil {
			s.d.Notify(ownerID, "files.changed", nil)
		}
	}
}

func (s *State) GetJob(id string) *model.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job := s.jobs[id]
	return job.Clone()
}

func (s *State) SetJobFields(id string, fields map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	if job == nil {
		return
	}
	applyJobFields(job, fields)
	s.saveLocked()
	if s.d.Notify != nil {
		s.d.Notify(job.OwnerID, "files.changed", map[string]any{"job_id": id})
	}
}

func (s *State) AppendJobPreviewLine(id, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	const maxPreviewChars = 40000

	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	if job == nil {
		return
	}

	prev := strings.TrimSpace(job.PreviewText)
	if prev == "" {
		prev = line
	} else {
		prev = prev + "\n" + line
	}
	if len(prev) > maxPreviewChars {
		prev = prev[len(prev)-maxPreviewChars:]
	}

	job.PreviewText = prev
	if s.d.SaveJobBlob != nil {
		if err := s.d.SaveJobBlob(id, "preview", []byte(prev)); err != nil && s.d.Errf != nil {
			s.d.Errf("state.savePreviewBlob", err, "job_id=%s", id)
		}
	}
}

func (s *State) ReplaceJobPreviewText(id, text string) {
	text = strings.TrimSpace(text)
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	if job == nil {
		return
	}
	job.PreviewText = text
	if s.d.SaveJobBlob != nil {
		if err := s.d.SaveJobBlob(id, "preview", []byte(text)); err != nil && s.d.Errf != nil {
			s.d.Errf("state.savePreviewBlob", err, "job_id=%s", id)
		}
	}
}

func (s *State) UploadedTS(id string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job := s.jobs[id]
	if job == nil {
		return 0
	}
	return job.UploadedTS
}

func (s *State) RemoveTagFromOwnerJobs(ownerID, tagName string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, job := range s.jobs {
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
	if !changed {
		return
	}
	s.saveLocked()
	if s.d.Notify != nil {
		s.d.Notify(ownerID, "files.changed", nil)
	}
}

// MarkSubtreeJobsTrashed mirrors the legacy behavior in app/runtime.go.
func (s *State) MarkSubtreeJobsTrashed(ownerID string, folderSet map[string]struct{}, normalizeFolderID func(string) string) {
	if s == nil || ownerID == "" || len(folderSet) == 0 {
		return
	}
	deletedTS := float64(s.d.Now().Unix())

	s.mu.Lock()
	defer s.mu.Unlock()
	for id, job := range s.jobs {
		if job.OwnerID != ownerID {
			continue
		}
		if _, ok := folderSet[normalizeFolderID(job.FolderID)]; ok {
			job.IsTrashed = true
			job.DeletedTS = deletedTS
			hydrateJobDerivedFields(job)
			if s.d.CancelJob != nil {
				s.d.CancelJob(id)
			}
			if s.d.RemoveTempWav != nil {
				s.d.RemoveTempWav(id)
			}
		}
	}
	s.saveLocked()
	if s.d.Notify != nil {
		s.d.Notify(ownerID, "files.changed", nil)
	}
}

func (s *State) saveLocked() {
	if s == nil || s.d.SaveJobs == nil {
		return
	}
	if err := s.d.SaveJobs(s.jobs); err != nil && s.d.Errf != nil {
		s.d.Errf("state.saveJobs", err, "save to db failed")
	}
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
			// derived
		case "media_duration":
			// derived
		case "media_duration_seconds":
			job.MediaDurationSeconds = intutil.AsIntPtr(v)
		case "description":
			job.Description = intutil.AsString(v)
		case "client_upload_id":
			job.ClientUploadID = intutil.AsString(v)
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
			// derived unless explicitly set; keep legacy behavior of ignoring.
		case "preview_text":
			job.PreviewText = intutil.AsString(v)
		case "result_refined":
			job.ResultRefined = intutil.AsString(v)
		case "status_detail":
			job.StatusDetail = intutil.AsString(v)
		case "page_count":
			job.PageCount = intutil.AsInt(v)
		case "processed_page_count":
			job.ProcessedPageCount = intutil.AsInt(v)
		case "current_chunk":
			job.CurrentChunk = intutil.AsInt(v)
		case "total_chunks":
			job.TotalChunks = intutil.AsInt(v)
		case "resume_available":
			job.ResumeAvailable = intutil.AsBool(v)
		}
	}
	hydrateJobDerivedFields(job)
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
