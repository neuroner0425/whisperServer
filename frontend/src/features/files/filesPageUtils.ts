import type { FolderNode, JobItem } from './types'
import type {
  DateFilter,
  DragState,
  FileListJob,
  FilesPageProps,
  PendingUpload,
  SelectionBox,
  SortDirection,
  SortKey,
  TypeFilter,
  VisibleEntry,
} from './filesPageTypes'
import { matchesDateFilter } from './filesPageDateUtils'
import { buildJobStatusText } from '../jobs/jobStatusText'

const naturalCompareOptions = { numeric: true } as const

export function currentFolderName(folderId: string, allFolders: FolderNode[]) {
  if (!folderId) {
    return '내 파일'
  }
  return allFolders.find((folder) => folder.ID === folderId)?.Name || '내 파일'
}

export function stripExtension(filename: string) {
  const dotIndex = filename.lastIndexOf('.')
  return dotIndex > 0 ? filename.slice(0, dotIndex) : filename
}

export function displayFilename(filename: string) {
  const ext = filename.split('.').pop()?.toLowerCase() || ''
  if (ext === 'mp3' || ext === 'wav' || ext === 'm4a') {
    return stripExtension(filename)
  }
  return filename
}

export function formatPendingStatus(stage: PendingUpload['stage'], progress: number) {
  if (stage === 'queued') {
    return '작업 대기 중'
  }
  if (stage === 'processing') {
    return `업로드 처리 중 ${Math.max(0, progress)}%`
  }
  if (stage === 'failed') {
    return '업로드 실패'
  }
  return `업로드 중 ${Math.max(0, progress)}%`
}

export function formatBytes(bytes?: number) {
  if (!bytes || bytes <= 0) {
    return '-'
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

export function formatJobSub(job: FileListJob) {
  if (job.__pending) {
    return job.Status
  }
  return buildJobStatusText(job)
}

export function normalizePage(value: string | null): number {
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 1
}

export function clampMenu(value: number) {
  return Math.max(12, Math.min(value, window.innerWidth - 196))
}

export function entryKey(kind: 'file' | 'folder', id: string) {
  return `${kind}:${id}`
}

export function updateQuery(
  searchParams: URLSearchParams,
  setSearchParams: (params: URLSearchParams) => void,
  updates: Record<string, string>,
) {
  const next = new URLSearchParams(searchParams)
  Object.entries(updates).forEach(([key, value]) => {
    if (value) {
      next.set(key, value)
    } else {
      next.delete(key)
    }
  })
  setSearchParams(next)
}

export function sortJobs(items: JobItem[], sortKey: SortKey | 'location', sortDirection: SortDirection) {
  return [...items].sort((a, b) => compareJob(a, b, sortKey, sortDirection))
}

export function sortEntries(entries: VisibleEntry[], sortKey: SortKey, sortDirection: SortDirection) {
  return [...entries].sort((a, b) => {
    if (a.kind !== b.kind) {
      return a.kind === 'folder' ? -1 : 1
    }
    let value = 0
    if (sortKey === 'name') {
      value = getEntryLabel(a).localeCompare(getEntryLabel(b), 'ko', naturalCompareOptions)
    } else if (sortKey === 'kind') {
      value = a.kind.localeCompare(b.kind, 'ko')
    } else if (sortKey === 'location') {
      value = getEntryLocation(a).localeCompare(getEntryLocation(b), 'ko', naturalCompareOptions)
    } else {
      value = compareDate(getEntryUpdatedAt(a), getEntryUpdatedAt(b))
    }
    return sortDirection === 'asc' ? value : -value
  })
}

export function matchesFolderFilters(folder: FolderNode, dateFilter: DateFilter) {
  return matchesDateFilter(folder.UpdatedAt, dateFilter)
}

export function matchesJobFilters(job: JobItem, dateFilter: DateFilter) {
  return matchesDateFilter(job.UpdatedAt, dateFilter)
}

export function renderSortMark(currentKey: SortKey, currentDirection: SortDirection, targetKey: SortKey) {
  if (currentKey !== targetKey) {
    return ''
  }
  return currentDirection === 'desc' ? ' ↓' : ' ↑'
}

export function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(value, max))
}

export function normalizeRect(box: SelectionBox) {
  return {
    left: Math.min(box.startX, box.currentX),
    top: Math.min(box.startY, box.currentY),
    right: Math.max(box.startX, box.currentX),
    bottom: Math.max(box.startY, box.currentY),
  }
}

export function rectanglesIntersect(selectionRect: { left: number; top: number; right: number; bottom: number }, rect: DOMRect) {
  return !(
    selectionRect.right < rect.left ||
    selectionRect.left > rect.right ||
    selectionRect.bottom < rect.top ||
    selectionRect.top > rect.bottom
  )
}

export function selectionBoxStyle(box: SelectionBox) {
  const rect = normalizeRect(box)
  return {
    left: `${rect.left}px`,
    top: `${rect.top}px`,
    width: `${rect.right - rect.left}px`,
    height: `${rect.bottom - rect.top}px`,
  }
}

export function readDragPayload(dataTransfer: DataTransfer): DragState | null {
  const payload = dataTransfer.getData('application/x-whisper-entries')
  if (!payload) {
    return null
  }
  try {
    const parsed = JSON.parse(payload) as Partial<DragState>
    return {
      jobIds: Array.isArray(parsed.jobIds) ? parsed.jobIds : [],
      folderIds: Array.isArray(parsed.folderIds) ? parsed.folderIds : [],
    }
  } catch {
    return null
  }
}

export function buildMovePath(allFolders: FolderNode[], folderID: string) {
  const path: Array<{ id: string; label: string }> = [{ id: '', label: '내 파일' }]
  if (!folderID) {
    return path
  }
  const byId = new Map(allFolders.map((folder) => [folder.ID, folder]))
  const stack: FolderNode[] = []
  let current = byId.get(folderID)
  while (current) {
    stack.unshift(current)
    current = current.ParentID ? byId.get(current.ParentID) : undefined
  }
  stack.forEach((folder) => {
    path.push({ id: folder.ID, label: folder.Name })
  })
  return path
}

export function typeFilterLabel(typeFilter: TypeFilter) {
  if (typeFilter === 'folder') {
    return '폴더'
  }
  if (typeFilter === 'document') {
    return '문서'
  }
  return '유형'
}

export function defaultSortState(viewMode: FilesPageProps['viewMode']) {
  if (viewMode === 'home') {
    return { key: 'updated' as const, direction: 'asc' as const }
  }
  if (viewMode === 'search') {
    return { key: 'updated' as const, direction: 'desc' as const }
  }
  return { key: 'name' as const, direction: 'asc' as const }
}

export function fileTypeLabel(fileType?: string) {
  const normalized = (fileType || '').trim()
  if (!normalized) {
    return '파일'
  }
  return normalized.toUpperCase()
}

function compareDate(a?: string, b?: string) {
  const aTime = Date.parse(a || '') || 0
  const bTime = Date.parse(b || '') || 0
  return bTime - aTime
}

function compareJob(a: JobItem, b: JobItem, sortKey: SortKey | 'location', sortDirection: SortDirection) {
  let value = 0
  if (sortKey === 'name') {
    value = a.Filename.localeCompare(b.Filename, 'ko', naturalCompareOptions)
  } else if (sortKey === 'location') {
    value = (a.FolderName || '').localeCompare(b.FolderName || '', 'ko', naturalCompareOptions)
  } else {
    value = compareDate(a.UpdatedAt, b.UpdatedAt)
  }
  return sortDirection === 'asc' ? value : -value
}

function getEntryLabel(entry: VisibleEntry) {
  return entry.kind === 'folder' ? entry.item.Name : entry.item.Filename
}

function getEntryUpdatedAt(entry: VisibleEntry) {
  return entry.kind === 'folder' ? entry.item.UpdatedAt : entry.item.UpdatedAt
}

function getEntryLocation(entry: VisibleEntry) {
  if (entry.kind === 'folder') {
    return entry.item.ParentID ? '하위 폴더' : '내 파일'
  }
  return entry.item.FolderName || '내 파일'
}
