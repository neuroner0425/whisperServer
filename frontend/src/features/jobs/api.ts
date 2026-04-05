import type { DocumentResponse, JobDetailResponse } from './types'

type JobActionResponse = {
  job_id: string
  status: string
  will_refine?: boolean
}

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

export async function retryJob(jobId: string) {
  const response = await fetch(`/api/jobs/${jobId}/retry`, {
    method: 'POST',
    headers: {
      Accept: 'application/json',
    },
  })
  if (response.status === 401) {
    window.location.href = '/auth/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    let message = `Failed to retry job (${response.status})`
    try {
      const payload = (await response.json()) as { detail?: string; message?: string }
      message = payload.detail || payload.message || message
    } catch {
      // ignore parse failure
    }
    throw new Error(message)
  }
  return (await response.json()) as JobActionResponse
}

export async function retranscribeJob(jobId: string) {
  const response = await fetch(`/api/jobs/${jobId}/retranscribe`, {
    method: 'POST',
    headers: {
      Accept: 'application/json',
    },
  })
  if (response.status === 401) {
    window.location.href = '/auth/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    let message = `Failed to retranscribe job (${response.status})`
    try {
      const payload = (await response.json()) as { detail?: string; message?: string }
      message = payload.detail || payload.message || message
    } catch {
      // ignore parse failure
    }
    throw new Error(message)
  }
  return (await response.json()) as JobActionResponse
}

export async function refineJob(jobId: string) {
  const response = await fetch(`/api/jobs/${jobId}/refine`, {
    method: 'POST',
    headers: {
      Accept: 'application/json',
    },
  })
  if (response.status === 401) {
    window.location.href = '/auth/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    let message = `Failed to refine job (${response.status})`
    try {
      const payload = (await response.json()) as { detail?: string; message?: string }
      message = payload.detail || payload.message || message
    } catch {
      // ignore parse failure
    }
    throw new Error(message)
  }
  return (await response.json()) as JobActionResponse
}

export async function rerefineJob(jobId: string) {
  const response = await fetch(`/api/jobs/${jobId}/rerefine`, {
    method: 'POST',
    headers: {
      Accept: 'application/json',
    },
  })
  if (response.status === 401) {
    window.location.href = '/auth/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    let message = `Failed to rerefine job (${response.status})`
    try {
      const payload = (await response.json()) as { detail?: string; message?: string }
      message = payload.detail || payload.message || message
    } catch {
      // ignore parse failure
    }
    throw new Error(message)
  }
  return (await response.json()) as JobActionResponse
}

export async function fetchDocumentJSON(url: string, signal?: AbortSignal): Promise<DocumentResponse> {
  const response = await fetch(url, {
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
    throw new Error(`Failed to load document json (${response.status})`)
  }
  return (await response.json()) as DocumentResponse
}
