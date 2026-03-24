import type { DateFilter } from './filesPageTypes'

export function extractDate(value?: string) {
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

export function matchesDateFilter(value: string | undefined, dateFilter: DateFilter) {
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
