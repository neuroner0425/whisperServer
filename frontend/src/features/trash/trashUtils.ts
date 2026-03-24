import type { TrashJobItem } from './api'

export type DateFilter = 'all' | 'past_hour' | 'today' | 'past_7_days' | 'past_30_days' | 'this_year' | 'last_year'
export type SortKey = 'name' | 'deleted' | 'location' | 'updated'
export type SortDirection = 'asc' | 'desc'

export const DATE_OPTIONS: Array<{ value: DateFilter; label: string }> = [
  { value: 'past_hour', label: '지난 1시간' },
  { value: 'today', label: '오늘' },
  { value: 'past_7_days', label: '지난 7일' },
  { value: 'past_30_days', label: '지난 30일' },
  { value: 'this_year', label: '올해' },
  { value: 'last_year', label: '지난 해' },
]

export function sortTrashJobs(items: TrashJobItem[], sortKey: SortKey, sortDirection: SortDirection) {
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

export function matchesTrashFilters(job: TrashJobItem, dateFilter: DateFilter) {
  return matchesDateFilter(job.DeletedAt, dateFilter)
}

export function renderSortMark(currentKey: SortKey, currentDirection: SortDirection, targetKey: SortKey) {
  if (currentKey !== targetKey) {
    return ''
  }
  return currentDirection === 'desc' ? ' ↓' : ' ↑'
}

export function formatTableDate(value?: string) {
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

export function displayFilename(filename: string) {
  const dotIndex = filename.lastIndexOf('.')
  const ext = dotIndex > 0 ? filename.slice(dotIndex + 1).toLowerCase() : ''
  if (ext === 'mp3' || ext === 'wav' || ext === 'm4a') {
    return dotIndex > 0 ? filename.slice(0, dotIndex) : filename
  }
  return filename
}

export function dateFilterLabel(dateFilter: DateFilter) {
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
