export type Tag = {
  Name: string
  Description: string
}

async function parse(response: Response) {
  const data = await response.json().catch(() => ({}))
  if (response.status === 401) {
    window.location.href = '/app/login'
    throw new Error('인증이 필요합니다.')
  }
  if (!response.ok) {
    throw new Error(data.detail || `Request failed (${response.status})`)
  }
  return data
}

export async function fetchTags(): Promise<Tag[]> {
  const response = await fetch('/api/tags', { headers: { Accept: 'application/json' } })
  const data = (await parse(response)) as { tags: Tag[] }
  return data.tags
}

export async function createTag(name: string, description: string) {
  const response = await fetch('/api/tags', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ name, description }),
  })
  return parse(response)
}

export async function deleteTag(name: string) {
  const response = await fetch(`/api/tags/${encodeURIComponent(name)}`, {
    method: 'DELETE',
    headers: { Accept: 'application/json' },
  })
  return parse(response)
}

export async function updateJobTags(jobId: string, tags: string[]) {
  const response = await fetch(`/api/jobs/${jobId}/tags`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ tags }),
  })
  return parse(response)
}
