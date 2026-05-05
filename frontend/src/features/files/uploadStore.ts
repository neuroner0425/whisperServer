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

function buildPendingUpload(uploadState: UploadState): PendingUpload {
  const clientUploadId = buildClientUploadId()
  return {
    localId: `local-${clientUploadId}`,
    clientUploadId,
    folderId: uploadState.folderId,
    filename: normalizeUploadFilename(uploadState),
    fileType: uploadState.fileType,
    stage: 'waiting',
    progress: 0,
  }
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

function isServerUploadFinalizing(job: JobItem) {
  const phase = (job.Phase || '').trim()
  return phase === '업로드 처리 중' || phase === '파일을 변환하는 중'
}

export function reconcilePendingUploads(serverJobs: JobItem[]) {
  let changed = false
  const next = pendingUploads.flatMap((item) => {
    const serverJob = serverJobs.find((job) => matchesPendingUpload(job, item))
    if (!serverJob) {
      return [item]
    }
    if (isServerUploadFinalizing(serverJob)) {
      const updated: PendingUpload = {
        ...item,
        jobId: serverJob.ID,
        stage: 'converting',
        progress: 0,
      }
      changed = changed || updated.jobId !== item.jobId || updated.stage !== item.stage || updated.progress !== item.progress
      return [updated]
    }
    changed = true
    return []
  })
  if (changed || next.length !== pendingUploads.length) {
    setPendingUploads(next)
  }
}

export function enqueuePendingUploads(uploadStates: UploadState[]) {
  const items = uploadStates.map(buildPendingUpload)
  setPendingUploads([...items, ...pendingUploads])
  return items
}

export async function startPendingUpload(uploadState: UploadState, pendingUpload?: PendingUpload) {
  const pendingItem = pendingUpload ?? buildPendingUpload(uploadState)
  const clientUploadId = pendingItem.clientUploadId
  const formData = new FormData()
  formData.append('file', uploadState.file)
  formData.append('display_name', uploadState.displayName)
  formData.append('folder_id', uploadState.folderId)
  formData.append('description', uploadState.description)
  formData.append('refine', String(uploadState.refineEnabled))
  formData.append('client_upload_id', clientUploadId)

  if (!pendingUploads.some((item) => item.localId === pendingItem.localId)) {
    setPendingUploads([pendingItem, ...pendingUploads])
  }
  updatePendingUpload(pendingItem.localId, (item) => ({ ...item, stage: 'uploading', progress: 0 }))

  try {
    const response = await uploadFileWithProgress(formData, (percent) => {
      updatePendingUpload(pendingItem.localId, (item) => {
        if (percent >= 100) {
          return { ...item, progress: 100, stage: 'finishing' }
        }
        return { ...item, progress: Math.max(0, Math.min(percent, 99)), stage: 'uploading' }
      })
    })
    updatePendingUpload(pendingItem.localId, (item) => ({
      ...item,
      jobId: response.job_id,
      clientUploadId: response.client_upload_id || item.clientUploadId,
      stage: 'converting',
      progress: 0,
    }))
    return response
  } catch (error) {
    updatePendingUpload(pendingItem.localId, (item) => ({ ...item, stage: 'failed', progress: 0 }))
    throw error
  }
}
