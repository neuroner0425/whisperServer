import type { FormEvent } from 'react'
import { NavLink, Outlet, useNavigate, useSearchParams } from 'react-router-dom'

import { logout } from './features/auth/api'

const navItems = [
  { to: '/files/home', label: '홈', end: true },
  { to: '/files/root', label: '내 파일' },
  { to: '/files/trash', label: '휴지통' },
]

export function AppShell() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const queryInput = searchParams.get('q') ?? ''

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
