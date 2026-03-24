import { useSyncExternalStore } from 'react'

import { uploadFileWithProgress } from './api'
import type { PendingUpload, UploadState } from './filesPageTypes'

type Listener = () => void

let pendingUploads: PendingUpload[] = []
const listeners = new Set<Listener>()

function emit() {
  listeners.forEach((listener) => listener())
}

function setPendingUploads(next: PendingUpload[]) {
  pendingUploads = next
  emit()
}

function updatePendingUpload(localId: string, updater: (item: PendingUpload) => PendingUpload) {
  let changed = false
  const next = pendingUploads.map((item) => {
    if (item.localId !== localId) {
      return item
    }
    changed = true
    return updater(item)
  })
  if (changed) {
    setPendingUploads(next)
  }
}

export function subscribePendingUploads(listener: Listener) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getPendingUploadsSnapshot() {
  return pendingUploads
}

export function usePendingUploads() {
  return useSyncExternalStore(subscribePendingUploads, getPendingUploadsSnapshot, getPendingUploadsSnapshot)
}

export function prunePendingUploads(serverJobIDs: Set<string>) {
  const next = pendingUploads.filter((item) => {
    if (!item.jobId) {
      return true
    }
    return !serverJobIDs.has(item.jobId)
  })
  if (next.length !== pendingUploads.length) {
    setPendingUploads(next)
  }
}

export async function startPendingUpload(uploadState: UploadState) {
  const formData = new FormData()
  formData.append('file', uploadState.file)
  formData.append('display_name', uploadState.displayName)
  formData.append('folder_id', uploadState.folderId)
  formData.append('description', uploadState.description)
  formData.append('refine', String(uploadState.refineEnabled))

  const localId = `upload-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  const pendingItem: PendingUpload = {
    localId,
    folderId: uploadState.folderId,
    filename: uploadState.displayName,
    stage: 'uploading',
    progress: 0,
  }
  setPendingUploads([pendingItem, ...pendingUploads])

  try {
    const response = await uploadFileWithProgress(formData, (percent) => {
      updatePendingUpload(localId, (item) => ({ ...item, progress: percent, stage: 'uploading' }))
    })
    updatePendingUpload(localId, (item) => ({
      ...item,
      jobId: response.job_id,
      stage: 'queued',
      progress: 0,
    }))
    return response
  } catch (error) {
    updatePendingUpload(localId, (item) => ({ ...item, stage: 'failed', progress: 0 }))
    throw error
  }
}
