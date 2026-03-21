package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"whisperserver/src/internal/model"
	"whisperserver/src/internal/store"
	"whisperserver/src/internal/worker"
)

type Runtime struct {
	jobsMu sync.RWMutex
	jobs   map[string]*model.Job
}

var (
	runtimeState = &Runtime{jobs: map[string]*model.Job{}}
	appWorker    *worker.Worker
	eventBroker  = newUserEventBroker()
)

func jobsSnapshot() map[string]*model.Job {
	runtimeState.jobsMu.RLock()
	defer runtimeState.jobsMu.RUnlock()
	out := make(map[string]*model.Job, len(runtimeState.jobs))
	for id, job := range runtimeState.jobs {
		out[id] = job.Clone()
	}
	return out
}

func addJob(id string, job *model.Job) {
	runtimeState.jobsMu.Lock()
	defer runtimeState.jobsMu.Unlock()
	hydrateJobDerivedFields(job)
	runtimeState.jobs[id] = job
	saveJobsLocked()
	if job != nil {
		eventBroker.Notify(job.OwnerID, "files.changed", map[string]any{"job_id": id})
	}
}

func tempWavPath(jobID string) string {
	return filepath.Join(tmpFolder, jobID+".wav")
}

func removeTempWav(jobID string) {
	if strings.TrimSpace(jobID) == "" {
		return
	}
	_ = os.Remove(tempWavPath(jobID))
}

func cleanupInactiveTempWavs() {
	runtimeState.jobsMu.RLock()
	active := make(map[string]struct{}, len(runtimeState.jobs))
	for id, job := range runtimeState.jobs {
		if job == nil || job.IsTrashed {
			continue
		}
		if job.Status == statusPending || job.Status == statusRunning {
			active[id] = struct{}{}
		}
	}
	runtimeState.jobsMu.RUnlock()

	entries, err := os.ReadDir(tmpFolder)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".wav" {
			continue
		}
		jobID := strings.TrimSuffix(entry.Name(), ".wav")
		if _, ok := active[jobID]; ok {
			continue
		}
		_ = os.Remove(filepath.Join(tmpFolder, entry.Name()))
	}
}

func collectFolderSubtree(userID string, folderIDs []string, trashFolders bool) map[string]struct{} {
	allFolders, _ := store.ListAllFoldersByOwner(userID, false)
	selectedFolders := make(map[string]struct{}, len(folderIDs))
	for _, id := range folderIDs {
		id = normalizeFolderID(id)
		if id == "" {
			continue
		}
		f, err := store.GetFolderByID(userID, id)
		if err != nil || f.IsTrashed {
			continue
		}
		selectedFolders[id] = struct{}{}
		if trashFolders {
			_ = store.SetFolderTrashed(userID, id, true)
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

func markSubtreeJobsTrashed(userID string, subtree map[string]struct{}) {
	if len(subtree) == 0 {
		return
	}
	deletedTS := float64(time.Now().Unix())
	runtimeState.jobsMu.Lock()
	defer runtimeState.jobsMu.Unlock()
	for id, job := range runtimeState.jobs {
		if job.OwnerID != userID {
			continue
		}
		if _, ok := subtree[normalizeFolderID(job.FolderID)]; ok {
			job.IsTrashed = true
			job.DeletedTS = deletedTS
			hydrateJobDerivedFields(job)
			cancelJob(id)
			removeTempWav(id)
		}
	}
	saveJobsLocked()
	eventBroker.Notify(userID, "files.changed", nil)
}
