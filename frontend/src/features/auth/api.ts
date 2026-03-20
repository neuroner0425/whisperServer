type AuthPayload = {
  identifier?: string
  login_id?: string
  email?: string
  password: string
}

async function submit(path: string, payload: AuthPayload) {
  const response = await fetch(path, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify(payload),
  })
  const data = await response.json().catch(() => ({}))
  if (!response.ok) {
    throw new Error(data.detail || `Request failed (${response.status})`)
  }
  return data
}

export function login(payload: Required<Pick<AuthPayload, 'identifier' | 'password'>>) {
  return submit('/api/auth/login', payload)
}

export function signup(payload: Required<Pick<AuthPayload, 'login_id' | 'email' | 'password'>>) {
  return submit('/api/auth/signup', payload)
}

export async function logout() {
  const response = await fetch('/api/auth/logout', {
    method: 'POST',
    headers: {
      Accept: 'application/json',
    },
  })
  if (!response.ok) {
    throw new Error('로그아웃에 실패했습니다.')
  }
}
