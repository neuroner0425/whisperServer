import type { FilesResponse } from './types'

type FetchFilesArgs = {
  viewMode: 'home' | 'explore' | 'search'
  folderId?: string
  query: string
  tag: string
  sort: 'name' | 'updated'
  order: 'asc' | 'desc'
  page: number
  version?: string
  signal?: AbortSignal
}

export async function fetchFiles({
  viewMode,
  folderId,
  query,
  tag,
  sort,
  order,
  page,
  version,
  signal,
}: FetchFilesArgs): Promise<FilesResponse | null> {
  const params = new URLSearchParams({
    view: viewMode,
    q: query,
    tag,
    sort,
    order,
    page: String(page),
  })
  if (folderId) {
    params.set('folder_id', folderId)
  }
  if (version) {
    params.set('v', version)
  }

  const response = await fetch(`/api/files?${params.toString()}`, {
    headers: {
      Accept: 'application/json',
    },
    signal,
  })
  if (response.status === 401) {
    window.location.href = '/auth/login'
    return null
  }
  if (!response.ok) {
    throw new Error(`Failed to load files view (${response.status})`)
  }

  const payload = (await response.json()) as FilesResponse
  if (payload.changed === false) {
    return null
  }
  return payload
}

async function parseJSONOrThrow(response: Response) {
  if (response.status === 401) {
    window.location.href = '/auth/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    let message = `Request failed (${response.status})`
    try {
      const payload = (await response.json()) as { message?: string; detail?: string }
      message = payload.message || payload.detail || message
    } catch {
      // ignore parse failure
    }
    throw new Error(message)
  }
  return response.json()
}

export async function createFolder(name: string, parentId: string) {
  const response = await fetch('/api/folders', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify({ name, parent_id: parentId }),
  })
  return parseJSONOrThrow(response)
}

export async function renameFolder(folderId: string, name: string) {
  const response = await fetch(`/api/folders/${folderId}`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify({ name }),
  })
  return parseJSONOrThrow(response)
}

export async function trashFolder(folderId: string) {
  const response = await fetch(`/api/folders/${folderId}`, {
    method: 'DELETE',
    headers: {
      Accept: 'application/json',
    },
  })
  return parseJSONOrThrow(response)
}

export async function renameJob(jobId: string, name: string) {
  const response = await fetch(`/api/jobs/${jobId}`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify({ name }),
  })
  return parseJSONOrThrow(response)
}

export async function trashJob(jobId: string) {
  const response = await fetch(`/api/jobs/${jobId}`, {
    method: 'DELETE',
    headers: {
      Accept: 'application/json',
    },
  })
  return parseJSONOrThrow(response)
}

export async function uploadFile(formData: FormData) {
  const response = await fetch('/api/upload', {
    method: 'POST',
    headers: {
      Accept: 'application/json',
    },
    body: formData,
  })
  return parseJSONOrThrow(response)
}

export function uploadFileWithProgress(
  formData: FormData,
  onProgress: (percent: number) => void,
): Promise<{ job_id: string; filename?: string; job_url?: string; client_upload_id?: string }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/upload')
    xhr.responseType = 'json'
    xhr.setRequestHeader('Accept', 'application/json')

    xhr.upload.onprogress = (event) => {
      if (!event.lengthComputable) {
        return
      }
      onProgress(Math.max(0, Math.min(100, Math.round((event.loaded / event.total) * 100))))
    }

    xhr.onerror = () => reject(new Error('업로드에 실패했습니다.'))
    xhr.ontimeout = () => reject(new Error('업로드 응답이 지연되었습니다.'))
    xhr.onload = () => {
      if (xhr.status === 401) {
        window.location.href = '/auth/login'
        reject(new Error('인증이 필요합니다.'))
        return
      }
      let payload = xhr.response as
        | { detail?: string; message?: string; job_id?: string; filename?: string; job_url?: string; client_upload_id?: string }
        | null
      if ((!payload || typeof payload !== 'object') && xhr.responseText) {
        try {
          payload = JSON.parse(xhr.responseText) as {
            detail?: string
            message?: string
            job_id?: string
            filename?: string
            job_url?: string
            client_upload_id?: string
          }
        } catch {
          payload = null
        }
      }
      if (xhr.status < 200 || xhr.status >= 300 || !payload?.job_id) {
        reject(new Error(payload?.detail || payload?.message || `Request failed (${xhr.status})`))
        return
      }
      resolve({
        job_id: payload.job_id,
        filename: payload.filename,
        job_url: payload.job_url,
        client_upload_id: payload.client_upload_id,
      })
    }

    xhr.send(formData)
  })
}

export async function fetchJobStatus(
  jobId: string,
  signal?: AbortSignal,
): Promise<{ status: string; progress_percent: number; phase: string; progress_label?: string; preview_text?: string }> {
  const response = await fetch(`/status/${jobId}`, {
    headers: { Accept: 'application/json' },
    signal,
  })
  if (response.status === 401) {
    window.location.href = '/auth/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    throw new Error(`Failed to load job status (${response.status})`)
  }
  return (await response.json()) as { status: string; progress_percent: number; phase: string; progress_label?: string; preview_text?: string }
}

export async function moveEntries(jobIds: string[], folderIds: string[], targetFolderId: string) {
  const response = await fetch('/api/move', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify({
      job_ids: jobIds,
      folder_ids: folderIds,
      target_folder_id: targetFolderId,
    }),
  })
  return parseJSONOrThrow(response)
}

export async function downloadFolder(folderId: string) {
  window.location.href = `/api/folders/${folderId}/download`
}

export function batchDownloadJobs(jobIds: string[]) {
  if (jobIds.length === 0) {
    return
  }
  const form = document.createElement('form')
  form.method = 'POST'
  form.action = '/batch-download'
  form.style.display = 'none'

  jobIds.forEach((jobId) => {
    const input = document.createElement('input')
    input.type = 'hidden'
    input.name = 'job_ids'
    input.value = jobId
    form.appendChild(input)
  })

  document.body.appendChild(form)
  form.submit()
  document.body.removeChild(form)
}
