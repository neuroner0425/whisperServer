export type TrashJobItem = {
  ID: string
  Filename: string
  FileType?: string
  SizeBytes?: number
  UpdatedAt: string
  DeletedAt: string
  FolderName: string
}

async function parse(response: Response) {
  const data = await response.json().catch(() => ({}))
  if (response.status === 401) {
    window.location.href = '/auth/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    throw new Error(data.detail || `Request failed (${response.status})`)
  }
  return data
}

export async function fetchTrash() {
  const response = await fetch('/api/trash', { headers: { Accept: 'application/json' } })
  return parse(response) as Promise<{ job_items: TrashJobItem[] }>
}

export async function restoreJob(jobId: string) {
  const response = await fetch(`/api/jobs/${jobId}/restore`, {
    method: 'POST',
    headers: { Accept: 'application/json' },
  })
  return parse(response)
}

export async function deleteTrashJobs(jobIds: string[]) {
  const response = await fetch('/api/trash/jobs/delete', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify({ job_ids: jobIds }),
  })
  return parse(response)
}

export async function clearTrash() {
  const response = await fetch('/api/trash/clear', {
    method: 'POST',
    headers: { Accept: 'application/json' },
  })
  return parse(response)
}
