import { useEffect, useMemo, useState } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'

import { clearTrash, deleteTrashJobs, fetchTrash, restoreJob } from './api'
import type { TrashJobItem } from './api'

type DateFilter = 'all' | 'past_hour' | 'today' | 'past_7_days' | 'past_30_days' | 'this_year' | 'last_year'
type SortKey = 'name' | 'deleted' | 'location' | 'updated'
type SortDirection = 'asc' | 'desc'
type FilterMenu = 'date' | null

const DATE_OPTIONS: Array<{ value: DateFilter; label: string }> = [
  { value: 'past_hour', label: '지난 1시간' },
  { value: 'today', label: '오늘' },
  { value: 'past_7_days', label: '지난 7일' },
  { value: 'past_30_days', label: '지난 30일' },
  { value: 'this_year', label: '올해' },
  { value: 'last_year', label: '지난 해' },
]

export function TrashPage() {
  const [jobs, setJobs] = useState<TrashJobItem[]>([])
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [selectedJobIds, setSelectedJobIds] = useState<string[]>([])
  const [selectionAnchor, setSelectionAnchor] = useState<string | null>(null)
  const [dateFilter, setDateFilter] = useState<DateFilter>('all')
  const [sortKey, setSortKey] = useState<SortKey>('deleted')
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc')
  const [filterMenu, setFilterMenu] = useState<FilterMenu>(null)

  const load = async () => {
    try {
      const data = await fetchTrash()
      setJobs(data.job_items)
      setError('')
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '휴지통을 불러오지 못했습니다.')
    }
  }

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load()
    }, 0)
    return () => window.clearTimeout(timer)
  }, [])

  useEffect(() => {
    if (!message && !error) {
      return
    }
    const timer = window.setTimeout(() => {
      setMessage('')
      setError('')
    }, 2800)
    return () => window.clearTimeout(timer)
  }, [error, message])

  useEffect(() => {
    if (!filterMenu) {
      return
    }
    const close = () => setFilterMenu(null)
    window.addEventListener('click', close)
    window.addEventListener('resize', close)
    window.addEventListener('scroll', close, true)
    return () => {
      window.removeEventListener('click', close)
      window.removeEventListener('resize', close)
      window.removeEventListener('scroll', close, true)
    }
  }, [filterMenu])

  const filteredJobs = useMemo(
    () =>
      sortTrashJobs(
        jobs.filter((job) => matchesTrashFilters(job, dateFilter)),
        sortKey,
        sortDirection,
      ),
    [dateFilter, jobs, sortDirection, sortKey],
  )

  const clearSelection = () => {
    setSelectedJobIds([])
    setSelectionAnchor(null)
  }

  const resetFilters = () => {
    setDateFilter('all')
    setFilterMenu(null)
  }

  const toggleSort = (nextKey: SortKey) => {
    if (sortKey === nextKey) {
      setSortDirection((current) => (current === 'desc' ? 'asc' : 'desc'))
      return
    }
    setSortKey(nextKey)
    setSortDirection('asc')
  }

  const handleEntryClick = (jobId: string, event: ReactMouseEvent<HTMLElement>) => {
    if (event.shiftKey && selectionAnchor) {
      const anchorIndex = filteredJobs.findIndex((job) => job.ID === selectionAnchor)
      const targetIndex = filteredJobs.findIndex((job) => job.ID === jobId)
      if (anchorIndex >= 0 && targetIndex >= 0) {
        const start = Math.min(anchorIndex, targetIndex)
        const end = Math.max(anchorIndex, targetIndex)
        setSelectedJobIds(filteredJobs.slice(start, end + 1).map((job) => job.ID))
        return
      }
    }

    if (event.metaKey || event.ctrlKey) {
      setSelectedJobIds((current) => (current.includes(jobId) ? current.filter((id) => id !== jobId) : [...current, jobId]))
      setSelectionAnchor(jobId)
      return
    }

    setSelectedJobIds([jobId])
    setSelectionAnchor(jobId)
  }

  const handleRestoreSelection = async () => {
    try {
      for (const jobId of selectedJobIds) {
        await restoreJob(jobId)
      }
      setMessage(`${selectedJobIds.length}개 파일을 복구했습니다.`)
      clearSelection()
      await load()
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '파일 복구에 실패했습니다.')
    }
  }

  const handleDeleteSelection = async () => {
    try {
      await deleteTrashJobs(selectedJobIds)
      setMessage(`${selectedJobIds.length}개 파일을 완전 삭제했습니다.`)
      clearSelection()
      await load()
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '파일 삭제에 실패했습니다.')
    }
  }

  return (
    <section className="view-shell">
      <section
        className="content-surface"
        onClick={(event) => {
          if (event.target === event.currentTarget) {
            clearSelection()
          }
        }}
      >
        <section className="drive-section">
          <div className="drive-pathbar">
            <div className="drive-path">
              <span className="drive-path-segment static">휴지통</span>
            </div>
            <div className="drive-path-meta">
              <button className="ghost-button danger small" onClick={() => void clearTrash()} type="button">
                휴지통 비우기
              </button>
            </div>
          </div>

          {selectedJobIds.length > 0 ? (
            <div className="selection-toolbar">
              <div className="selection-toolbar-inner">
                <div className="selection-toolbar-main">
                  <button className="selection-toolbar-close" onClick={() => clearSelection()} type="button">
                    ×
                  </button>
                  <div className="selection-toolbar-copy">{selectedJobIds.length}개 선택됨</div>
                </div>
                <div className="selection-toolbar-actions">
                  <button className="toolbar-button" onClick={() => void handleRestoreSelection()} type="button">
                    복구
                  </button>
                  <button className="toolbar-button danger" onClick={() => void handleDeleteSelection()} type="button">
                    완전 삭제
                  </button>
                </div>
              </div>
            </div>
          ) : (
            <div className="filter-toolbar">
              <div className="filter-group">
                <div className="filter-control">
                  <button
                    className={`filter-toggle-button${dateFilter !== 'all' ? ' active' : ''}`}
                    onClick={(event) => {
                      event.stopPropagation()
                      setFilterMenu((current) => (current === 'date' ? null : 'date'))
                    }}
                    type="button"
                  >
                    <span>{dateFilterLabel(dateFilter)}</span>
                    <span className="filter-toggle-caret">▾</span>
                  </button>
                  {filterMenu === 'date' ? (
                    <div className="filter-menu">
                      {DATE_OPTIONS.map((option) => (
                        <button
                          className={`filter-menu-item${dateFilter === option.value ? ' active' : ''}`}
                          key={option.value}
                          onClick={(event) => {
                            event.stopPropagation()
                            setDateFilter(option.value)
                            setFilterMenu(null)
                          }}
                          type="button"
                        >
                          {option.label}
                        </button>
                      ))}
                    </div>
                  ) : null}
                </div>
                {dateFilter !== 'all' ? (
                  <button className="filter-clear-button" onClick={() => setDateFilter('all')} type="button">
                    ×
                  </button>
                ) : null}
              </div>
              {dateFilter !== 'all' ? (
                <div className="filter-group">
                  <button className="filter-reset-button" onClick={resetFilters} type="button">
                    필터 지우기
                  </button>
                </div>
              ) : null}
            </div>
          )}

          <div
            className="drive-table trash-table"
            onClick={(event) => {
              if (event.target === event.currentTarget) {
                clearSelection()
              }
            }}
          >
            <div className="drive-table-header trash">
              <button className="column-sort-button" onClick={() => toggleSort('name')} type="button">
                파일 명{renderSortMark(sortKey, sortDirection, 'name')}
              </button>
              <button className="column-sort-button" onClick={() => toggleSort('deleted')} type="button">
                삭제 날짜{renderSortMark(sortKey, sortDirection, 'deleted')}
              </button>
              <button className="column-sort-button" onClick={() => toggleSort('location')} type="button">
                위치{renderSortMark(sortKey, sortDirection, 'location')}
              </button>
              <button className="column-sort-button" onClick={() => toggleSort('updated')} type="button">
                최근 수정 일자{renderSortMark(sortKey, sortDirection, 'updated')}
              </button>
            </div>
            {filteredJobs.map((job) => (
              <div
                className={`drive-table-row trash${selectedJobIds.includes(job.ID) ? ' selected' : ''}`}
                key={job.ID}
                onClick={(event) => handleEntryClick(job.ID, event)}
              >
                <div className="drive-table-primary trash-primary" role="button" tabIndex={0}>
                    <span className="drive-item-icon">🎧</span>
                    <span className="drive-item-copy">
                    <span className="drive-item-title">{displayFilename(job.Filename)}</span>
                    </span>
                  </div>
                <span className="drive-table-meta">{formatTableDate(job.DeletedAt)}</span>
                <span className="drive-table-meta">{job.FolderName || '내 파일'}</span>
                <span className="drive-table-meta">{formatTableDate(job.UpdatedAt)}</span>
              </div>
            ))}
            {filteredJobs.length === 0 ? <div className="empty-panel">삭제된 파일이 없습니다.</div> : null}
          </div>
        </section>

        {error || message ? (
          <div className="toast-stack">
            {error ? <div className="alert error toast-alert">{error}</div> : null}
            {message ? <div className="alert info toast-alert">{message}</div> : null}
          </div>
        ) : null}
      </section>
    </section>
  )
}

function sortTrashJobs(items: TrashJobItem[], sortKey: SortKey, sortDirection: SortDirection) {
  return [...items].sort((a, b) => {
    let value = 0
    if (sortKey === 'name') {
      value = a.Filename.localeCompare(b.Filename, 'ko')
    } else if (sortKey === 'location') {
      value = (a.FolderName || '').localeCompare(b.FolderName || '', 'ko')
    } else if (sortKey === 'updated') {
      value = compareDate(a.UpdatedAt, b.UpdatedAt)
    } else {
      value = compareDate(a.DeletedAt, b.DeletedAt)
    }
    return sortDirection === 'asc' ? value : -value
  })
}

function matchesTrashFilters(job: TrashJobItem, dateFilter: DateFilter) {
  return matchesDateFilter(job.DeletedAt, dateFilter)
}

function matchesDateFilter(value: string | undefined, dateFilter: DateFilter) {
  if (dateFilter === 'all') {
    return true
  }
  const timestamp = Date.parse(value || '')
  if (Number.isNaN(timestamp)) {
    return false
  }
  const now = new Date()
  const diff = now.getTime() - timestamp
  switch (dateFilter) {
    case 'past_hour':
      return diff <= 60 * 60 * 1000
    case 'today': {
      const startOfDay = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
      return timestamp >= startOfDay
    }
    case 'past_7_days':
      return diff <= 7 * 24 * 60 * 60 * 1000
    case 'past_30_days':
      return diff <= 30 * 24 * 60 * 60 * 1000
    case 'this_year':
      return new Date(timestamp).getFullYear() === now.getFullYear()
    case 'last_year':
      return new Date(timestamp).getFullYear() === now.getFullYear() - 1
    default:
      return true
  }
}

function compareDate(a?: string, b?: string) {
  const aTime = Date.parse(a || '') || 0
  const bTime = Date.parse(b || '') || 0
  return bTime - aTime
}

function renderSortMark(currentKey: SortKey, currentDirection: SortDirection, targetKey: SortKey) {
  if (currentKey !== targetKey) {
    return ''
  }
  return currentDirection === 'desc' ? ' ↓' : ' ↑'
}

function formatTableDate(value?: string) {
  if (!value) {
    return '-'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return '-'
  }

  const now = new Date()
  const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const yesterdayStart = todayStart - 24 * 60 * 60 * 1000
  const timestamp = date.getTime()
  const time = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`

  if (timestamp >= todayStart) {
    return `오늘 ${time}`
  }
  if (timestamp >= yesterdayStart) {
    return `어제 ${time}`
  }

  return `${date.getFullYear()}년 ${String(date.getMonth() + 1).padStart(2, '0')}월 ${String(date.getDate()).padStart(2, '0')}일 ${time}`
}

function displayFilename(filename: string) {
  const dotIndex = filename.lastIndexOf('.')
  const ext = dotIndex > 0 ? filename.slice(dotIndex + 1).toLowerCase() : ''
  if (ext === 'mp3' || ext === 'wav' || ext === 'm4a') {
    return dotIndex > 0 ? filename.slice(0, dotIndex) : filename
  }
  return filename
}

function dateFilterLabel(dateFilter: DateFilter) {
  switch (dateFilter) {
    case 'past_hour':
      return '지난 1시간'
    case 'today':
      return '오늘'
    case 'past_7_days':
      return '지난 7일'
    case 'past_30_days':
      return '지난 30일'
    case 'this_year':
      return '올해'
    case 'last_year':
      return '지난 해'
    default:
      return '수정 날짜'
  }
}
