package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"whisperserver/src/internal/events"
	"whisperserver/src/internal/model"
	"whisperserver/src/internal/state"
	"whisperserver/src/internal/worker"
)

type Config struct {
	TmpFolder            string
	Now                  func() time.Time
	LoadJobs             func() (map[string]*model.Job, error)
	SaveJobs             func(map[string]*model.Job) error
	DeleteJobBlobs       func(string)
	SaveJobBlob          func(string, string, []byte) error
	ListAllFoldersByOwner func(string, bool) ([]model.Folder, error)
	GetFolderByID        func(string, string) (*model.Folder, error)
	SetFolderTrashed     func(string, string, bool) error
	Notify               func(string, string, map[string]any)
	Errf                 func(string, error, string, ...any)
}

type Runtime struct {
	state  *state.State
	broker *events.Broker
	worker *worker.Worker

	tmpFolder             string
	listAllFoldersByOwner func(string, bool) ([]model.Folder, error)
	getFolderByID         func(string, string) (*model.Folder, error)
	setFolderTrashed      func(string, string, bool) error
}

func New(cfg Config) *Runtime {
	broker := events.NewBroker()
	rt := &Runtime{
		broker:                broker,
		tmpFolder:             cfg.TmpFolder,
		listAllFoldersByOwner: cfg.ListAllFoldersByOwner,
		getFolderByID:         cfg.GetFolderByID,
		setFolderTrashed:      cfg.SetFolderTrashed,
	}
	rt.state = state.New(state.Deps{
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

func (r *Runtime) Broker() *events.Broker { return r.broker }

func (r *Runtime) SetWorker(w *worker.Worker) { r.worker = w }

func (r *Runtime) JobsSnapshot() map[string]*model.Job { return r.state.JobsSnapshot() }

func (r *Runtime) AddJob(id string, job *model.Job) { r.state.AddJob(id, job) }

func (r *Runtime) TempWavPath(jobID string) string { return filepath.Join(r.tmpFolder, jobID+".wav") }

func (r *Runtime) RemoveTempWav(jobID string) {
	if strings.TrimSpace(jobID) == "" {
		return
	}
	_ = os.Remove(r.TempWavPath(jobID))
}

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

func (r *Runtime) MarkSubtreeJobsTrashed(userID string, subtree map[string]struct{}) {
	r.state.MarkSubtreeJobsTrashed(userID, subtree, strings.TrimSpace)
}

func (r *Runtime) EnqueueTranscribe(jobID string) {
	if r.worker != nil {
		r.worker.EnqueueTranscribe(jobID)
	}
}

func (r *Runtime) EnqueueRefine(jobID string) {
	if r.worker != nil {
		r.worker.EnqueueRefine(jobID)
	}
}

func (r *Runtime) EnqueuePDFExtract(jobID string) {
	if r.worker != nil {
		r.worker.EnqueuePDFExtract(jobID)
	}
}

func (r *Runtime) CancelJob(jobID string) {
	if r.worker != nil {
		r.worker.Cancel(jobID)
	}
}

func (r *Runtime) RequeuePending() {
	if r.worker != nil {
		r.worker.RequeuePending(r.JobsSnapshot())
	}
}

func (r *Runtime) DeleteJobs(ids []string) { r.state.DeleteJobs(ids) }

func (r *Runtime) LoadJobs() { r.state.Load() }

func (r *Runtime) GetJob(id string) *model.Job { return r.state.GetJob(id) }

func (r *Runtime) SetJobFields(id string, fields map[string]any) { r.state.SetJobFields(id, fields) }

func (r *Runtime) RemoveTagFromOwnerJobs(ownerID, tagName string) {
	r.state.RemoveTagFromOwnerJobs(ownerID, tagName)
}

func (r *Runtime) AppendJobPreviewLine(id, line string) { r.state.AppendJobPreviewLine(id, line) }

func (r *Runtime) ReplaceJobPreviewText(id, text string) { r.state.ReplaceJobPreviewText(id, text) }

func (r *Runtime) UploadedTS(id string) float64 { return r.state.UploadedTS(id) }

var (
	previewTimelineRe = regexp.MustCompile(`^\s*\[\d{2}:\d{2}:\d{2}(?:\.\d+)?\s*-->\s*\d{2}:\d{2}:\d{2}(?:\.\d+)?\]\s*`)
	previewBracketRe  = regexp.MustCompile(`^\s*\[[^\]]+\]\s*`)
)

func SanitizePreviewLine(line string) string {
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

func SanitizePreviewText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if s := SanitizePreviewLine(line); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}
