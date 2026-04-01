import { useSyncExternalStore } from 'react'

import { uploadFileWithProgress } from './api'
import type { PendingUpload, UploadState } from './filesPageTypes'
import type { JobItem } from './types'

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

function buildClientUploadId() {
  return `upload-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
}

function normalizeUploadFilename(uploadState: UploadState) {
  const originalName = uploadState.file.name
  const extIndex = originalName.lastIndexOf('.')
  const ext = extIndex >= 0 ? originalName.slice(extIndex).toLowerCase() : ''
  const trimmedDisplayName = uploadState.displayName.trim()
  if (!trimmedDisplayName) {
    return originalName
  }
  return ext && !trimmedDisplayName.toLowerCase().endsWith(ext) ? `${trimmedDisplayName}${ext}` : trimmedDisplayName
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

export function matchesPendingUpload(job: JobItem, pendingUpload: PendingUpload) {
  if (pendingUpload.jobId && job.ID === pendingUpload.jobId) {
    return true
  }
  if (pendingUpload.clientUploadId && job.ClientUploadID === pendingUpload.clientUploadId) {
    return true
  }
  return false
}

export function prunePendingUploads(serverJobs: JobItem[]) {
  const next = pendingUploads.filter((item) => {
    return !serverJobs.some((job) => matchesPendingUpload(job, item))
  })
  if (next.length !== pendingUploads.length) {
    setPendingUploads(next)
  }
}

export async function startPendingUpload(uploadState: UploadState) {
  const clientUploadId = buildClientUploadId()
  const formData = new FormData()
  formData.append('file', uploadState.file)
  formData.append('display_name', uploadState.displayName)
  formData.append('folder_id', uploadState.folderId)
  formData.append('description', uploadState.description)
  formData.append('refine', String(uploadState.refineEnabled))
  formData.append('client_upload_id', clientUploadId)

  const localId = `local-${clientUploadId}`
  const pendingItem: PendingUpload = {
    localId,
    clientUploadId,
    folderId: uploadState.folderId,
    filename: normalizeUploadFilename(uploadState),
    stage: 'uploading',
    progress: 0,
  }
  setPendingUploads([pendingItem, ...pendingUploads])

  try {
    const response = await uploadFileWithProgress(formData, (percent) => {
      updatePendingUpload(localId, (item) => {
        if (percent >= 100) {
          return { ...item, progress: 98, stage: 'processing' }
        }
        return { ...item, progress: Math.min(percent, 98), stage: 'uploading' }
      })
    })
    updatePendingUpload(localId, (item) => ({
      ...item,
      jobId: response.job_id,
      clientUploadId: response.client_upload_id || item.clientUploadId,
      stage: 'queued',
      progress: 99,
    }))
    return response
  } catch (error) {
    updatePendingUpload(localId, (item) => ({ ...item, stage: 'failed', progress: 0 }))
    throw error
  }
}
