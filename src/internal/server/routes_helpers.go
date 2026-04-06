// routes_helpers.go exposes small runtime adapters used by transport wiring.
package server

import model "whisperserver/src/internal/domain"

// jobsSnapshot returns a defensive copy of the current runtime job snapshot.
func jobsSnapshot() map[string]*model.Job {
	if appRuntime == nil {
		return map[string]*model.Job{}
	}
	return appRuntime.JobsSnapshot()
}

// collectFolderSubtree proxies folder subtree expansion through the runtime object.
func collectFolderSubtree(userID string, folderIDs []string, trashFolders bool) map[string]struct{} {
	if appRuntime == nil {
		return map[string]struct{}{}
	}
	return appRuntime.CollectFolderSubtree(userID, folderIDs, trashFolders)
}

// markSubtreeJobsTrashed forwards subtree trash propagation into the runtime state store.
func markSubtreeJobsTrashed(userID string, subtree map[string]struct{}) {
	if appRuntime != nil {
		appRuntime.MarkSubtreeJobsTrashed(userID, subtree)
	}
}
