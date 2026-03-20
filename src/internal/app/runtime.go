package app

import (
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
	runtimeState.jobs[id] = job
	saveJobsLocked()
	if job != nil {
		eventBroker.Notify(job.OwnerID, "files.changed", map[string]any{"job_id": id})
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
	deletedAt := time.Now().Format("2006-01-02 15:04:05")
	runtimeState.jobsMu.Lock()
	defer runtimeState.jobsMu.Unlock()
	for id, job := range runtimeState.jobs {
		if job.OwnerID != userID {
			continue
		}
		if _, ok := subtree[normalizeFolderID(job.FolderID)]; ok {
			job.IsTrashed = true
			job.DeletedAt = deletedAt
			cancelJob(id)
		}
	}
	saveJobsLocked()
	eventBroker.Notify(userID, "files.changed", nil)
}
