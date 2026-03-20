import type { JobDetailResponse } from './types'

export async function fetchJobDetail(jobId: string, original: boolean, signal?: AbortSignal): Promise<JobDetailResponse> {
  const params = new URLSearchParams()
  if (original) {
    params.set('original', 'true')
  }

  const query = params.toString()
  const response = await fetch(`/api/jobs/${jobId}${query ? `?${query}` : ''}`, {
    headers: {
      Accept: 'application/json',
    },
    signal,
  })
  if (response.status === 401) {
    window.location.href = '/auth/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    throw new Error(`Failed to load job detail (${response.status})`)
  }
  return (await response.json()) as JobDetailResponse
}
