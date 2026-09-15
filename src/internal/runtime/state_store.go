// state_store.go owns the in-memory job snapshot and its derived-field maintenance.
package runtime

import (
	"container/list"
	"strings"
	"sync"
	"time"

	model "whisperserver/src/internal/domain"
	intutil "whisperserver/src/internal/util"
)

// stateDeps bundles persistence and side-effect hooks for the state store.
type stateDeps struct {
	Now func() time.Time

	LoadJobs            func() (map[string]*model.Job, error)
	GetJobByID          func(string) (*model.Job, error)
	GetOwnerIDsByJobIDs func([]string) ([]string, error)
	SaveJob             func(string, *model.Job) error
	DeleteJobsDB        func([]string) error
	SaveJobs            func(map[string]*model.Job) error
	DeleteJobBlobs      func(string)
	SaveJobBlob         func(string, string, []byte) error

	MarkJobsTrashedByFolderIDs func(ownerID string, folderIDs []string, deletedTS float64) error

	Notify        func(userID, eventType string, payload map[string]any)
	CancelJob     func(string)
	RemoveTempWav func(string)
	Errf          func(scope string, err error, format string, args ...any)
}

type lruEntry struct {
	key string
	job *model.Job
}

// completedJobCache is a thread-safe LRU cache for recently completed or queried jobs.
type completedJobCache struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	evict    *list.List
}

func newCompletedJobCache(capacity int) *completedJobCache {
	if capacity <= 0 {
		capacity = 128
	}
	return &completedJobCache{
		capacity: capacity,
		items:    make(map[string]*list.Element),
		evict:    list.New(),
	}
}

func (c *completedJobCache) Get(key string) *model.Job {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		c.evict.MoveToFront(elem)
		return elem.Value.(*lruEntry).job.Clone()
	}
	return nil
}

func (c *completedJobCache) Put(key string, job *model.Job) {
	if c == nil || job == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		c.evict.MoveToFront(elem)
		elem.Value.(*lruEntry).job = job.Clone()
		return
	}
	if c.evict.Len() >= c.capacity {
		oldest := c.evict.Back()
		if oldest != nil {
			c.evict.Remove(oldest)
			delete(c.items, oldest.Value.(*lruEntry).key)
		}
	}
	elem := c.evict.PushFront(&lruEntry{key: key, job: job.Clone()})
	c.items[key] = elem
}

func (c *completedJobCache) Remove(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		c.evict.Remove(elem)
		delete(c.items, key)
	}
}

// State owns the in-memory job snapshot and persists it via store deps.
// This is still process-local by design.
type stateStore struct {
	mu             sync.RWMutex
	jobs           map[string]*model.Job
	completedCache *completedJobCache
	d              stateDeps
}

// newStateStore builds the in-memory state store with its persistence callbacks.
func newStateStore(d stateDeps) *stateStore {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &stateStore{
		jobs:           map[string]*model.Job{},
		completedCache: newCompletedJobCache(128),
		d:              d,
	}
}

// JobsSnapshot returns a cloned view of the current job map.
func (s *stateStore) JobsSnapshot() map[string]*model.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*model.Job, len(s.jobs))
	for id, job := range s.jobs {
		out[id] = job.Clone()
	}
	return out
}

// Load hydrates the in-memory snapshot from persistent storage.
func (s *stateStore) Load() {
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

// shouldPersistJobFields determines whether field updates require a database write.
// Volatile/in-flight fields (phase, progress_percent, progress_label, preview_text,
// status_detail, page_count, processed_page_count, current_chunk, total_chunks, resume_available)
// are updated in-memory and broadcast via SSE without hitting the database.
func shouldPersistJobFields(fields map[string]any) bool {
	if len(fields) == 0 {
		return false
	}
	for k := range fields {
		switch k {
		case "phase", "progress_percent", "progress_label", "preview_text",
			"status_detail", "page_count", "processed_page_count",
			"current_chunk", "total_chunks", "resume_available":
			continue
		default:
			return true
		}
	}
	return false
}

// saveJobLocked persists a single job if SaveJob is provided, otherwise falls back to saveLocked.
func (s *stateStore) saveJobLocked(id string, job *model.Job) {
	if s == nil {
		return
	}
	if s.d.SaveJob != nil && job != nil {
		if err := s.d.SaveJob(id, job); err != nil && s.d.Errf != nil {
			s.d.Errf("state.saveJob", err, "save job to db failed job_id=%s", id)
		}
		return
	}
	s.saveLocked()
}

// AddJob inserts a new job, derives display fields, and emits a file-change notification.
func (s *stateStore) AddJob(id string, job *model.Job) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hydrateJobDerivedFields(job)
	s.jobs[id] = job
	s.saveJobLocked(id, job)
	if job != nil && s.d.Notify != nil {
		s.d.Notify(job.OwnerID, "files.changed", map[string]any{"job_id": id})
	}
}

// DeleteJobs removes jobs, cancels running work, and cleans related blobs/temp files.
func (s *stateStore) DeleteJobs(ids []string) {
	if s == nil || len(ids) == 0 {
		return
	}

	// 1. Identify owners of non-active/completed jobs from DB BEFORE deletion
	owners := map[string]struct{}{}
	if s.d.GetOwnerIDsByJobIDs != nil {
		if dbOwners, err := s.d.GetOwnerIDsByJobIDs(ids); err == nil {
			for _, o := range dbOwners {
				if o != "" {
					owners[o] = struct{}{}
				}
			}
		}
	}

	// 2. Clean in-memory active jobs under write lock
	s.mu.Lock()
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
		delete(s.jobs, id)
	}
	s.mu.Unlock()

	// 3. Invalidate LRU cache entries
	if s.completedCache != nil {
		for _, id := range ids {
			s.completedCache.Remove(id)
		}
	}

	// 4. Delete from persistent database
	if s.d.DeleteJobsDB != nil {
		if err := s.d.DeleteJobsDB(ids); err != nil && s.d.Errf != nil {
			s.d.Errf("state.deleteJobsDB", err, "delete jobs from db failed")
		}
	} else {
		s.mu.Lock()
		s.saveLocked()
		s.mu.Unlock()
	}

	// 5. Clean up attached blobs in object storage/DB
	if s.d.DeleteJobBlobs != nil {
		for _, id := range ids {
			s.d.DeleteJobBlobs(id)
		}
	}

	// 6. Broadcast files.changed SSE to all affected owners
	for ownerID := range owners {
		if s.d.Notify != nil {
			s.d.Notify(ownerID, "files.changed", nil)
		}
	}
}

// GetJob returns a cloned copy of a single job.
// It checks the active in-memory cache first, then the completed LRU cache, and falls back to DB.
func (s *stateStore) GetJob(id string) *model.Job {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	job := s.jobs[id]
	s.mu.RUnlock()
	if job != nil {
		return job.Clone()
	}

	if s.completedCache != nil {
		if cached := s.completedCache.Get(id); cached != nil {
			return cached
		}
	}

	if s.d.GetJobByID != nil {
		dbJob, err := s.d.GetJobByID(id)
		if err != nil && s.d.Errf != nil {
			s.d.Errf("state.getJobDB", err, "failed to get job %s from db", id)
		}
		if dbJob != nil {
			if s.completedCache != nil && !model.IsActiveStatusCode(dbJob.StatusCode) {
				s.completedCache.Put(id, dbJob)
			}
			return dbJob.Clone()
		}
	}
	return nil
}

// SetJobFields applies a partial update map to an existing job.
func (s *stateStore) SetJobFields(id string, fields map[string]any) {
	if s == nil {
		return
	}

	// Check if the job is active in RAM under read lock first
	s.mu.RLock()
	activeJob := s.jobs[id]
	s.mu.RUnlock()

	if activeJob != nil {
		// Fast-path for active in-memory job
		s.mu.Lock()
		job := s.jobs[id]
		if job != nil {
			applyJobFields(job, fields)
			if shouldPersistJobFields(fields) {
				s.saveJobLocked(id, job)
			}
			if model.IsActiveStatusCode(job.StatusCode) {
				s.jobs[id] = job
			} else {
				delete(s.jobs, id)
				if s.completedCache != nil {
					s.completedCache.Put(id, job)
				}
			}
			ownerID := job.OwnerID
			s.mu.Unlock()

			if s.d.Notify != nil {
				s.d.Notify(ownerID, "files.changed", map[string]any{"job_id": id})
			}
			return
		}
		s.mu.Unlock()
	}

	// Slow-path for completed/inactive jobs outside RAM: NO lock held on s.mu!
	if s.completedCache != nil {
		s.completedCache.Remove(id)
	}

	if s.d.GetJobByID == nil {
		return
	}
	dbJob, err := s.d.GetJobByID(id)
	if err != nil || dbJob == nil {
		return
	}

	applyJobFields(dbJob, fields)
	if shouldPersistJobFields(fields) && s.d.SaveJob != nil {
		if err := s.d.SaveJob(id, dbJob); err != nil && s.d.Errf != nil {
			s.d.Errf("state.saveJobDB", err, "failed to save job %s to db", id)
		}
	}

	if model.IsActiveStatusCode(dbJob.StatusCode) {
		// Transitioned back to active state: restore into memory
		s.mu.Lock()
		s.jobs[id] = dbJob
		s.mu.Unlock()
	} else if s.completedCache != nil {
		s.completedCache.Put(id, dbJob)
	}

	if s.d.Notify != nil {
		s.d.Notify(dbJob.OwnerID, "files.changed", map[string]any{"job_id": id})
	}
}

// UpdateProgress updates volatile in-flight progress in memory and emits notifications without DB writes.
func (s *stateStore) UpdateProgress(id string, phase string, percent int, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	if job == nil {
		return
	}
	job.Phase = phase
	job.ProgressPercent = percent
	if strings.TrimSpace(label) != "" {
		job.ProgressLabel = label
	} else {
		job.ProgressLabel = deriveJobProgressLabel(job)
	}
	if s.d.Notify != nil {
		s.d.Notify(job.OwnerID, "job.progress", map[string]any{
			"job_id":           id,
			"phase":            job.Phase,
			"progress_percent": job.ProgressPercent,
			"progress_label":   job.ProgressLabel,
		})
		s.d.Notify(job.OwnerID, "files.changed", map[string]any{
			"job_id":           id,
			"phase":            job.Phase,
			"progress_percent": job.ProgressPercent,
		})
	}
}

// TransitionStatus updates the lifecycle state, persists to DB, and emits notifications.
func (s *stateStore) TransitionStatus(id string, status string, statusCode int, extraFields map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	if job == nil {
		return
	}
	if statusCode != 0 {
		job.StatusCode = statusCode
		job.Status = model.JobStatusName(statusCode)
	} else if status != "" {
		job.Status = status
		job.StatusCode = model.JobStatusCode(status)
	}
	if len(extraFields) > 0 {
		applyJobFields(job, extraFields)
	} else {
		hydrateJobDerivedFields(job)
	}
	s.saveJobLocked(id, job)
	if s.d.Notify != nil {
		s.d.Notify(job.OwnerID, "job.status", map[string]any{
			"job_id":      id,
			"status":      job.Status,
			"status_code": job.StatusCode,
		})
		s.d.Notify(job.OwnerID, "files.changed", map[string]any{
			"job_id":      id,
			"status":      job.Status,
			"status_code": job.StatusCode,
		})
	}
	// Evict inactive (completed or failed) jobs from in-memory cache and insert into LRU cache
	if !model.IsActiveStatusCode(job.StatusCode) {
		delete(s.jobs, id)
		if s.completedCache != nil {
			s.completedCache.Put(id, job)
		}
	}
}

// MutateMetadata applies user metadata changes, persists them to DB, and emits notifications.
func (s *stateStore) MutateMetadata(id string, fields map[string]any) {
	s.SetJobFields(id, fields)
}

// GetActiveJob returns a clone of the job if it is currently in an active or in-flight state.
func (s *stateStore) GetActiveJob(id string) *model.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job := s.jobs[id]
	if job == nil {
		return nil
	}
	if model.IsActiveStatusCode(job.StatusCode) || job.ProgressPercent > 0 || strings.TrimSpace(job.Phase) != "" {
		return job.Clone()
	}
	return nil
}

// ActiveJobsSnapshot returns a snapshot map of jobs that are currently active in memory.
func (s *stateStore) ActiveJobsSnapshot() map[string]*model.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*model.Job)
	for id, job := range s.jobs {
		if model.IsActiveStatusCode(job.StatusCode) || job.ProgressPercent > 0 || strings.TrimSpace(job.Phase) != "" {
			out[id] = job.Clone()
		}
	}
	return out
}

// AppendJobPreviewLine appends a line to the preview text in memory while keeping it bounded.
func (s *stateStore) AppendJobPreviewLine(id, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	const maxPreviewChars = 2 * 1024 * 1024

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
}

// ReplaceJobPreviewText overwrites the preview text in memory.
func (s *stateStore) ReplaceJobPreviewText(id, text string) {
	text = strings.TrimSpace(text)
	const maxPreviewChars = 2 * 1024 * 1024
	if len(text) > maxPreviewChars {
		text = text[len(text)-maxPreviewChars:]
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	if job == nil {
		return
	}
	job.PreviewText = text
}

// UploadedTS returns the uploaded timestamp used by file queries and sorting.
func (s *stateStore) UploadedTS(id string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job := s.jobs[id]
	if job == nil {
		return 0
	}
	return job.UploadedTS
}

// RemoveTagFromOwnerJobs removes a deleted tag from every job owned by the user.
func (s *stateStore) RemoveTagFromOwnerJobs(ownerID, tagName string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changedJobs := map[string]*model.Job{}
	for id, job := range s.jobs {
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
			changedJobs[id] = job
		}
	}
	if len(changedJobs) == 0 {
		return
	}
	for id, job := range changedJobs {
		s.saveJobLocked(id, job)
	}
	if s.d.Notify != nil {
		s.d.Notify(ownerID, "files.changed", nil)
	}
}

// MarkSubtreeJobsTrashed mirrors folder-trash propagation into all descendant jobs.
func (s *stateStore) MarkSubtreeJobsTrashed(ownerID string, folderSet map[string]struct{}, normalizeFolderID func(string) string) {
	if s == nil || ownerID == "" || len(folderSet) == 0 {
		return
	}
	deletedTS := float64(s.d.Now().Unix())

	s.mu.Lock()
	defer s.mu.Unlock()
	changedJobs := map[string]*model.Job{}
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
			changedJobs[id] = job
		}
	}
	for id, job := range changedJobs {
		s.saveJobLocked(id, job)
	}

	// Also persist to DB for all jobs in folderSet (including inactive ones evicted from RAM)
	if s.d.MarkJobsTrashedByFolderIDs != nil {
		folderIDs := make([]string, 0, len(folderSet))
		for fid := range folderSet {
			folderIDs = append(folderIDs, fid)
		}
		if err := s.d.MarkJobsTrashedByFolderIDs(ownerID, folderIDs, deletedTS); err != nil && s.d.Errf != nil {
			s.d.Errf("state.markJobsTrashedByFolderIDs", err, "failed to mark subtree jobs trashed in db")
		}
	}

	if s.d.Notify != nil {
		s.d.Notify(ownerID, "files.changed", nil)
	}
}

// saveLocked persists the current snapshot while the caller still holds the write lock.
func (s *stateStore) saveLocked() {
	if s == nil || s.d.SaveJobs == nil {
		return
	}
	if err := s.d.SaveJobs(s.jobs); err != nil && s.d.Errf != nil {
		s.d.Errf("state.saveJobs", err, "save to db failed")
	}
}

// applyJobFields applies a partial field map while preserving derived fields afterward.
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

// parseJobTimestamp parses the timestamp formats still found in legacy update paths.
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

// hydrateJobDerivedFields refreshes derived status, timestamp, and duration display fields.
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

// formatJobTimestamp formats unix timestamps into the legacy display format.
func formatJobTimestamp(ts float64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(int64(ts), 0).Format("2006-01-02 15:04:05")
}

// formatDurationSeconds turns an optional second count into the UI duration string.
func formatDurationSeconds(sec *int) string {
	if sec == nil {
		return ""
	}
	return intutil.FormatSeconds(*sec)
}

// deriveJobDuration computes a wall-clock duration from start and completion timestamps.
func deriveJobDuration(startedTS, completedTS float64) string {
	if startedTS <= 0 || completedTS <= 0 || completedTS < startedTS {
		return ""
	}
	return intutil.FormatSeconds(int(completedTS - startedTS))
}

// deriveJobProgressLabel picks the most user-friendly progress label for the job.
func deriveJobProgressLabel(job *model.Job) string {
	if job == nil {
		return ""
	}
	if strings.TrimSpace(job.Phase) != "" {
		return job.Phase
	}
	return job.Status
}

// deriveJobPhase fills a fallback phase label from the job status code.
func deriveJobPhase(statusCode int) string {
	return model.JobPhase(statusCode)
}
