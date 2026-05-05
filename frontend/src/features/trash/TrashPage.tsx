import { useEffect, useMemo, useState } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'

import { clearTrash as clearTrashAPI, deleteTrashJobs, fetchTrash, restoreJob } from './api'
import type { TrashJobItem } from './api'
import { formatCompactDate } from '../files/filesPageDateUtils'
import { formatBytes } from '../files/filesPageUtils'
import { usePageTitle } from '../../usePageTitle'
import {
  DATE_OPTIONS,
  dateFilterLabel,
  displayFilename,
  formatTableDate,
  matchesTrashFilters,
  renderSortMark,
  sortTrashJobs,
  type DateFilter,
  type SortDirection,
  type SortKey,
} from './trashUtils'

type FilterMenu = 'date' | null

export function TrashPage() {
  usePageTitle('Trash')
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
      setError(normalizeLoadError(loadError, '휴지통을 불러오지 못했습니다.'))
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
    if (isPersistentNetworkError(error)) {
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

  useEffect(() => {
    const handleSelectAll = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'a' || event.altKey) {
        return
      }
      const target = event.target as HTMLElement | null
      if (target?.closest('input, textarea, select, [contenteditable="true"]') || filteredJobs.length === 0) {
        return
      }
      event.preventDefault()
      const nextIds = filteredJobs.map((job) => job.ID)
      setSelectedJobIds(nextIds)
      setSelectionAnchor(nextIds[0] ?? null)
    }

    window.addEventListener('keydown', handleSelectAll)
    return () => {
      window.removeEventListener('keydown', handleSelectAll)
    }
  }, [filteredJobs])

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
    if (!window.confirm(`${selectedJobIds.length}개 파일을 완전삭제할까요? 이 작업은 되돌릴 수 없습니다.`)) {
      return
    }
    try {
      await deleteTrashJobs(selectedJobIds)
      setMessage(`${selectedJobIds.length}개 파일을 완전삭제했습니다.`)
      clearSelection()
      await load()
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '파일 완전삭제에 실패했습니다.')
    }
  }

  const handleClearTrash = async () => {
    if (!window.confirm('휴지통의 모든 항목을 완전삭제할까요? 이 작업은 되돌릴 수 없습니다.')) {
      return
    }
    try {
      await clearTrashAPI()
      setJobs([])
      clearSelection()
      setMessage('휴지통을 비웠습니다.')
      await load()
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '휴지통 비우기에 실패했습니다.')
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
              <button className="ghost-button danger small" onClick={() => void handleClearTrash()} type="button">
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
                    완전삭제
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
              <span>종류</span>
              <button className="column-sort-button" onClick={() => toggleSort('deleted')} type="button">
                삭제 날짜{renderSortMark(sortKey, sortDirection, 'deleted')}
              </button>
              <button className="column-sort-button" onClick={() => toggleSort('location')} type="button">
                위치{renderSortMark(sortKey, sortDirection, 'location')}
              </button>
              <span>파일 크기</span>
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
                    <span className="drive-item-sub">휴지통</span>
                    </span>
                  </div>
                <div className="drive-table-mobile-meta">
                  <span className="drive-table-meta mobile-type-meta">{job.FileType ? job.FileType.toUpperCase() : 'FILE'}</span>
                  <span className="drive-table-meta mobile-location-meta">{job.FolderName || '내 파일'}</span>
                  <span className="drive-table-meta mobile-size-meta">{formatBytes(job.SizeBytes)}</span>
                  <span className="drive-table-meta mobile-date-meta">{formatCompactDate(job.DeletedAt)}</span>
                </div>
                <span className="drive-table-meta mobile-type-source">{job.FileType ? job.FileType.toUpperCase() : 'FILE'}</span>
                <span className="drive-table-meta mobile-date-source">{formatTableDate(job.DeletedAt)}</span>
                <span className="drive-table-meta mobile-location-source">{job.FolderName || '내 파일'}</span>
                <span className="drive-table-meta mobile-size-source">{formatBytes(job.SizeBytes)}</span>
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

function normalizeLoadError(error: unknown, fallback: string) {
  if (error instanceof Error && error.message === 'Failed to fetch') {
    return '서버와 연결할 수 없습니다.'
  }
  return error instanceof Error ? error.message : fallback
}

function isPersistentNetworkError(error: string) {
  return error === 'Failed to fetch' || error.includes('서버와 연결할 수 없습니다.')
}
