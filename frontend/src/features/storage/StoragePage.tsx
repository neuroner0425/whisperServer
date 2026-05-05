import { useEffect, useMemo, useState } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'

import { dateFilterLabel, matchesDateFilter } from '../files/filesPageDateUtils'
import { displayFilename, fileTypeLabel, matchesJobTypeFilter, typeFilterLabel } from '../files/filesPageUtils'
import { DATE_OPTIONS, TYPE_OPTIONS, type DateFilter, type TypeFilter } from '../files/filesPageTypes'
import { batchDownloadJobs } from '../files/api'
import { deleteJobsPermanently, fetchStorage, type StorageItem } from './api'
import { usePageTitle } from '../../usePageTitle'

type SortKey = 'name' | 'size'
type SortDirection = 'asc' | 'desc'
type FilterMenu = 'type' | 'date' | null

function formatBytes(bytes: number) {
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

export function StoragePage() {
  usePageTitle('Storage')
  const [data, setData] = useState<{ capacity_bytes: number; used_bytes: number; used_ratio: number; items: StorageItem[] } | null>(null)
  const [error, setError] = useState('')
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all')
  const [dateFilter, setDateFilter] = useState<DateFilter>('all')
  const [sortKey, setSortKey] = useState<SortKey>('size')
  const [sortDirection, setSortDirection] = useState<SortDirection>('desc')
  const [filterMenu, setFilterMenu] = useState<FilterMenu>(null)
  const [selectedItemIds, setSelectedItemIds] = useState<string[]>([])
  const [selectionAnchor, setSelectionAnchor] = useState<string | null>(null)

  useEffect(() => {
    const load = async () => {
      try {
        const payload = await fetchStorage()
        setData(payload)
        setError('')
      } catch (loadError) {
        setError(normalizeLoadError(loadError, '저장용량 정보를 불러오지 못했습니다.'))
      }
    }
    void load()
  }, [])

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

  const filteredItems = useMemo(() => {
    const source = data?.items ?? []
    const filtered = source.filter((item) => {
      if (typeFilter === 'folder') {
        return false
      }
      return matchesJobTypeFilter({ FileType: item.file_type }, typeFilter) && matchesDateFilter(item.updated_at, dateFilter)
    })
    return [...filtered].sort((a, b) => {
      let value = 0
      if (sortKey === 'name') {
        value = a.filename.localeCompare(b.filename, 'ko')
      } else {
        value = a.size_bytes - b.size_bytes
      }
      return sortDirection === 'asc' ? value : -value
    })
  }, [data?.items, dateFilter, sortDirection, sortKey, typeFilter])

  useEffect(() => {
    const handleSelectAll = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'a' || event.altKey) {
        return
      }
      const target = event.target as HTMLElement | null
      if (target?.closest('input, textarea, select, [contenteditable="true"]') || filteredItems.length === 0) {
        return
      }
      event.preventDefault()
      const nextIds = filteredItems.map((item) => item.id)
      setSelectedItemIds(nextIds)
      setSelectionAnchor(nextIds[0] ?? null)
    }

    window.addEventListener('keydown', handleSelectAll)
    return () => {
      window.removeEventListener('keydown', handleSelectAll)
    }
  }, [filteredItems])

  const toggleSort = (nextKey: SortKey) => {
    if (sortKey === nextKey) {
      setSortDirection((current) => (current === 'desc' ? 'asc' : 'desc'))
      return
    }
    setSortKey(nextKey)
    setSortDirection(nextKey === 'size' ? 'desc' : 'asc')
  }

  const renderSortMark = (targetKey: SortKey) => {
    if (sortKey !== targetKey) {
      return ''
    }
    return sortDirection === 'desc' ? ' ↓' : ' ↑'
  }

  const usedBytes = data?.used_bytes ?? 0
  const capacityBytes = data?.capacity_bytes ?? 0
  const usedRatio = Math.max(0, Math.min(100, Math.round((data?.used_ratio ?? 0) * 100)))

  const handleItemClick = (itemId: string, event: ReactMouseEvent<HTMLElement>) => {
    if (event.shiftKey && selectionAnchor) {
      const anchorIndex = filteredItems.findIndex((item) => item.id === selectionAnchor)
      const targetIndex = filteredItems.findIndex((item) => item.id === itemId)
      if (anchorIndex >= 0 && targetIndex >= 0) {
        const start = Math.min(anchorIndex, targetIndex)
        const end = Math.max(anchorIndex, targetIndex)
        setSelectedItemIds(filteredItems.slice(start, end + 1).map((item) => item.id))
        return
      }
    }
    if (event.metaKey || event.ctrlKey) {
      setSelectedItemIds((current) => (current.includes(itemId) ? current.filter((id) => id !== itemId) : [...current, itemId]))
      setSelectionAnchor(itemId)
      return
    }
    setSelectedItemIds([itemId])
    setSelectionAnchor(itemId)
  }

  const clearSelection = () => {
    setSelectedItemIds([])
    setSelectionAnchor(null)
  }

  const handleDeleteSelection = async () => {
    if (!window.confirm(`${selectedItemIds.length}개 파일을 완전삭제할까요? 이 작업은 되돌릴 수 없습니다.`)) {
      return
    }
    try {
      await deleteJobsPermanently(selectedItemIds)
      const payload = await fetchStorage()
      setData(payload)
      clearSelection()
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '파일 완전삭제에 실패했습니다.')
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
              <span className="drive-path-segment static">저장용량</span>
            </div>
          </div>

          <section className="storage-overview">
            <div className="storage-usage-line">
              <strong>{formatBytes(usedBytes)}</strong>
              <span> / {formatBytes(capacityBytes)} 사용 중</span>
            </div>
            <div className="storage-progress-track" role="presentation">
              <div className="storage-progress-fill" style={{ width: `${usedRatio}%` }} />
            </div>
          </section>

          {selectedItemIds.length > 0 ? (
            <div className="selection-toolbar">
              <div className="selection-toolbar-inner">
                <div className="selection-toolbar-main">
                  <button className="selection-toolbar-close" onClick={() => clearSelection()} type="button">
                    ×
                  </button>
                  <div className="selection-toolbar-copy">{selectedItemIds.length}개 선택됨</div>
                </div>
                <div className="selection-toolbar-actions">
                  <button className="toolbar-button" onClick={() => batchDownloadJobs(selectedItemIds)} type="button">
                    다운로드
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
                    className={`filter-toggle-button${typeFilter !== 'all' ? ' active' : ''}`}
                    onClick={(event) => {
                      event.stopPropagation()
                      setFilterMenu((current) => (current === 'type' ? null : 'type'))
                    }}
                    type="button"
                  >
                    <span>{typeFilterLabel(typeFilter)}</span>
                    <span className="filter-toggle-caret">▾</span>
                  </button>
                  {filterMenu === 'type' ? (
                    <div className="filter-menu">
                      {TYPE_OPTIONS.map((option) => (
                        <button
                          className={`filter-menu-item${typeFilter === option.value ? ' active' : ''}`}
                          key={option.value}
                          onClick={(event) => {
                            event.stopPropagation()
                            setTypeFilter(option.value)
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
                {typeFilter !== 'all' ? (
                  <button className="filter-clear-button" onClick={() => setTypeFilter('all')} type="button">
                    ×
                  </button>
                ) : null}
              </div>

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
            </div>
          )}

          {error ? <div className="inline-error">{error}</div> : null}

          <div className="drive-table storage-table">
            <div className="drive-table-header">
              <button className="column-sort-button" onClick={() => toggleSort('name')} type="button">
                이름{renderSortMark('name')}
              </button>
              <button className="column-sort-button align-right" onClick={() => toggleSort('size')} type="button">
                사용한 용량{renderSortMark('size')}
              </button>
            </div>
            <div className="drive-table-body">
              {filteredItems.map((item) => (
                <div
                  className={`drive-row${selectedItemIds.includes(item.id) ? ' selected' : ''}`}
                  key={item.id}
                  onClick={(event) => handleItemClick(item.id, event)}
                  onDoubleClick={() => (window.location.href = `/file/${item.id}`)}
                >
                  <button className="drive-row-main" type="button">
                    <span className="drive-item-icon" aria-hidden="true">
                      🎧
                    </span>
                    <span className="drive-row-copy">
                      <strong>{displayFilename(item.filename)}</strong>
                      <span>{fileTypeLabel(item.file_type)}</span>
                    </span>
                  </button>
                  <div className="drive-row-size">{formatBytes(item.size_bytes)}</div>
                </div>
              ))}
            </div>
          </div>
        </section>
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
