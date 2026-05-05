export type StorageItem = {
  id: string
  filename: string
  file_type: string
  folder_name: string
  updated_at: string
  size_bytes: number
}

export type StorageResponse = {
  capacity_bytes: number
  used_bytes: number
  available_bytes: number
  used_ratio: number
  items: StorageItem[]
}

export async function fetchStorage(): Promise<StorageResponse> {
  const response = await fetch('/api/storage', {
    headers: { Accept: 'application/json' },
  })
  if (response.status === 401) {
    window.location.href = '/auth/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    throw new Error(`저장용량 정보를 불러오지 못했습니다. (${response.status})`)
  }
  return (await response.json()) as StorageResponse
}

export async function deleteJobsPermanently(jobIds: string[]) {
  const response = await fetch('/api/jobs/delete', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify({ job_ids: jobIds }),
  })
  if (response.status === 401) {
    window.location.href = '/auth/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    throw new Error(`파일 완전삭제에 실패했습니다. (${response.status})`)
  }
  return response.json()
}
