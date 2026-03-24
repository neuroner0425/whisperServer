import type { FormEvent } from 'react'
import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom'

import { logout } from './features/auth/api'
import { fetchStorage } from './features/storage/api'

const navItems = [
  { to: '/files/home', label: '홈', end: true },
  { to: '/files/root', label: '내 파일' },
  { to: '/files/trash', label: '휴지통' },
  { to: '/files/storage', label: '저장용량' },
]

export function AppShell() {
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams] = useSearchParams()
  const queryInput = searchParams.get('q') ?? ''
  const [storageSummary, setStorageSummary] = useState<{ used: number; capacity: number } | null>(null)

  useEffect(() => {
    const loadStorageSummary = async () => {
      try {
        const payload = await fetchStorage()
        setStorageSummary({ used: payload.used_bytes, capacity: payload.capacity_bytes })
      } catch {
        setStorageSummary(null)
      }
    }

    void loadStorageSummary()

    const source = new EventSource('/api/events')
    source.addEventListener('update', (event) => {
      try {
        const payload = JSON.parse((event as MessageEvent<string>).data) as { type?: string }
        if (payload.type === 'files.changed') {
          void loadStorageSummary()
        }
      } catch {
        // ignore malformed events
      }
    })

    return () => {
      source.close()
    }
  }, [location.pathname])

  const handleLogout = async () => {
    await logout()
    navigate('/auth/login')
  }

  const handleSearch = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const next = String(formData.get('q') || '').trim()
    navigate(next ? `/files/search?q=${encodeURIComponent(next)}` : '/files/search')
  }

  const usedRatio = storageSummary?.capacity ? Math.max(0, Math.min(100, Math.round((storageSummary.used / storageSummary.capacity) * 100))) : 0
  const formatBytes = (bytes: number) => {
    if (bytes <= 0) {
      return '0B'
    }
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    let value = bytes
    let index = 0
    while (value >= 1024 && index < units.length - 1) {
      value /= 1024
      index += 1
    }
    const fixed = value >= 100 || index === 0 ? 0 : value >= 10 ? 1 : 2
    return `${value.toFixed(fixed)}${units[index]}`
  }

  return (
    <div className="workspace-shell">
      <aside className="workspace-sidebar">
        <div className="workspace-brand-block">
          <div className="workspace-brand">Whisper Drive</div>
          <div aria-hidden="true" className="workspace-subtitle" />
        </div>
        <button
          className="sidebar-create-button"
          onClick={() => window.dispatchEvent(new CustomEvent('whisper:new-file'))}
          type="button"
        >
          <span className="sidebar-create-plus">+</span>
          <span>새 파일</span>
        </button>
        <nav className="workspace-nav">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              className={({ isActive }) => `workspace-nav-link${isActive ? ' active' : ''}`}
              end={item.end}
              to={item.to}
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="storage-sidebar-widget">
          <div className="storage-sidebar-bar" role="presentation">
            <div className="storage-sidebar-fill" style={{ width: `${usedRatio}%` }} />
          </div>
          <div className="storage-sidebar-copy">
            {storageSummary ? `${formatBytes(storageSummary.capacity)} 중 ${formatBytes(storageSummary.used)} 사용` : '5GB 중 0B 사용'}
          </div>
        </div>
        <button className="ghost-button" onClick={() => void handleLogout()} type="button">
          로그아웃
        </button>
      </aside>
      <main className="workspace-main">
        <header className="workspace-topbar">
          <form className="drive-search app-drive-search" onSubmit={handleSearch}>
            <span className="drive-search-icon" aria-hidden="true">
              ⌕
            </span>
            <input
              className="drive-search-input"
              defaultValue={queryInput}
              key={queryInput}
              name="q"
              placeholder="내 파일에서 검색"
            />
          </form>
        </header>
        <Outlet />
      </main>
    </div>
  )
}
