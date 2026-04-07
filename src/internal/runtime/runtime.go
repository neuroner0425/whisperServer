// runtime.go exposes the process-local runtime facade used by HTTP and worker wiring.
package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/worker"
)

// Config wires runtime state to persistence, folders, and notifications.
type Config struct {
	TmpFolder             string
	Now                   func() time.Time
	LoadJobs              func() (map[string]*model.Job, error)
	SaveJobs              func(map[string]*model.Job) error
	DeleteJobBlobs        func(string)
	SaveJobBlob           func(string, string, []byte) error
	ListAllFoldersByOwner func(string, bool) ([]model.Folder, error)
	GetFolderByID         func(string, string) (*model.Folder, error)
	SetFolderTrashed      func(string, string, bool) error
	Notify                func(string, string, map[string]any)
	Errf                  func(string, error, string, ...any)
}

// Runtime owns process-local job state, SSE broadcasting, and queue integration.
type Runtime struct {
	state  *stateStore
	broker *Broker
	worker *worker.Worker

	tmpFolder             string
	listAllFoldersByOwner func(string, bool) ([]model.Folder, error)
	getFolderByID         func(string, string) (*model.Folder, error)
	setFolderTrashed      func(string, string, bool) error
}

// New builds the runtime around the state store, broker, and folder helper callbacks.
func New(cfg Config) *Runtime {
	broker := NewBroker()
	rt := &Runtime{
		broker:                broker,
		tmpFolder:             cfg.TmpFolder,
		listAllFoldersByOwner: cfg.ListAllFoldersByOwner,
		getFolderByID:         cfg.GetFolderByID,
		setFolderTrashed:      cfg.SetFolderTrashed,
	}
	rt.state = newStateStore(stateDeps{
		Now:            cfg.Now,
		LoadJobs:       cfg.LoadJobs,
		SaveJobs:       cfg.SaveJobs,
		DeleteJobBlobs: cfg.DeleteJobBlobs,
		SaveJobBlob:    cfg.SaveJobBlob,
		Notify:         broker.Notify,
		CancelJob:      rt.CancelJob,
		RemoveTempWav:  rt.RemoveTempWav,
		Errf:           cfg.Errf,
	})
	return rt
}

// Broker returns the SSE broker attached to this process runtime.
func (r *Runtime) Broker() *Broker { return r.broker }

// SetWorker attaches the background worker so runtime can enqueue and cancel jobs.
func (r *Runtime) SetWorker(w *worker.Worker) { r.worker = w }

// JobsSnapshot returns a cloned view of the current in-memory job map.
func (r *Runtime) JobsSnapshot() map[string]*model.Job { return r.state.JobsSnapshot() }

// AddJob inserts a new job into the in-memory snapshot.
func (r *Runtime) AddJob(id string, job *model.Job) { r.state.AddJob(id, job) }

// TempWavPath returns the temporary wav path used during transcription.
func (r *Runtime) TempWavPath(jobID string) string { return filepath.Join(r.tmpFolder, jobID+".wav") }

// RemoveTempWav removes the temporary wav file for a job when it exists.
func (r *Runtime) RemoveTempWav(jobID string) {
	if strings.TrimSpace(jobID) == "" {
		return
	}
	_ = os.Remove(r.TempWavPath(jobID))
}

// CleanupInactiveTempWavs clears leftover wav/m4a temp files from previous runs.
func (r *Runtime) CleanupInactiveTempWavs() {
	entries, err := os.ReadDir(r.tmpFolder)
	if err != nil {
		return
	}
	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if entry.IsDir() || (ext != ".wav" && ext != ".m4a") {
			continue
		}
		_ = os.Remove(filepath.Join(r.tmpFolder, entry.Name()))
	}
}

// CollectFolderSubtree expands the selected folders to include every descendant folder id.
func (r *Runtime) CollectFolderSubtree(userID string, folderIDs []string, trashFolders bool) map[string]struct{} {
	allFolders, _ := r.listAllFoldersByOwner(userID, false)
	selectedFolders := make(map[string]struct{}, len(folderIDs))
	for _, id := range folderIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		f, err := r.getFolderByID(userID, id)
		if err != nil || f.IsTrashed {
			continue
		}
		selectedFolders[id] = struct{}{}
		if trashFolders {
			_ = r.setFolderTrashed(userID, id, true)
		}
	}
	subtree := make(map[string]struct{}, len(selectedFolders))
	for id := range selectedFolders {
		subtree[id] = struct{}{}
	}
	changed := true
	for changed {
		changed = false
		for _, f := range allFolders {
			if _, ok := subtree[f.ParentID]; ok {
				if _, exists := subtree[f.ID]; !exists {
					subtree[f.ID] = struct{}{}
					changed = true
				}
			}
		}
	}
	return subtree
}

// MarkSubtreeJobsTrashed propagates a folder trash operation into job snapshot state.
func (r *Runtime) MarkSubtreeJobsTrashed(userID string, subtree map[string]struct{}) {
	r.state.MarkSubtreeJobsTrashed(userID, subtree, strings.TrimSpace)
}

// EnqueueTranscribe forwards an audio transcription request to the worker.
func (r *Runtime) EnqueueTranscribe(jobID string) {
	if r.worker != nil {
		r.worker.EnqueueTranscribe(jobID)
	}
}

// EnqueueRefine forwards a refine request to the worker.
func (r *Runtime) EnqueueRefine(jobID string) {
	if r.worker != nil {
		r.worker.EnqueueRefine(jobID)
	}
}

// EnqueuePDFExtract forwards a PDF extraction request to the worker.
func (r *Runtime) EnqueuePDFExtract(jobID string) {
	if r.worker != nil {
		r.worker.EnqueuePDFExtract(jobID)
	}
}

// CancelJob cancels the currently running worker task for the supplied job id.
func (r *Runtime) CancelJob(jobID string) {
	if r.worker != nil {
		r.worker.Cancel(jobID)
	}
}

// RequeuePending re-enqueues jobs that were mid-flight when the process restarted.
func (r *Runtime) RequeuePending() {
	if r.worker != nil {
		r.worker.RequeuePending(r.JobsSnapshot())
	}
}

// DeleteJobs removes jobs from the in-memory snapshot and persistence side effects.
func (r *Runtime) DeleteJobs(ids []string) { r.state.DeleteJobs(ids) }

// LoadJobs loads the persisted snapshot into memory.
func (r *Runtime) LoadJobs() { r.state.Load() }

// GetJob returns a cloned job from the current in-memory snapshot.
func (r *Runtime) GetJob(id string) *model.Job { return r.state.GetJob(id) }

// SetJobFields applies partial updates to a job in the current snapshot.
func (r *Runtime) SetJobFields(id string, fields map[string]any) { r.state.SetJobFields(id, fields) }

// RemoveTagFromOwnerJobs removes a tag from every job owned by a user.
func (r *Runtime) RemoveTagFromOwnerJobs(ownerID, tagName string) {
	r.state.RemoveTagFromOwnerJobs(ownerID, tagName)
}

// AppendJobPreviewLine appends a single preview line to the job preview blob and snapshot.
func (r *Runtime) AppendJobPreviewLine(id, line string) { r.state.AppendJobPreviewLine(id, line) }

// ReplaceJobPreviewText replaces the entire preview text for a job.
func (r *Runtime) ReplaceJobPreviewText(id, text string) { r.state.ReplaceJobPreviewText(id, text) }

// UploadedTS returns the uploaded timestamp for sorting and snapshot versioning.
func (r *Runtime) UploadedTS(id string) float64 { return r.state.UploadedTS(id) }
