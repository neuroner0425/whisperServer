import type { FolderNode, JobItem } from './types'

export type FilesPageProps = {
  viewMode: 'home' | 'explore' | 'search'
}

export type MoveState =
  | { type: 'file'; id: string; name: string }
  | { type: 'folder'; id: string; name: string }

export type UploadState = {
  file: File
  displayName: string
  folderId: string
  description: string
  refineEnabled: boolean
}

export type TypeFilter = 'all' | 'folder' | 'document'
export type DateFilter = 'all' | 'past_hour' | 'today' | 'past_7_days' | 'past_30_days' | 'this_year' | 'last_year'
export type SortKey = 'name' | 'updated' | 'kind' | 'location'
export type SortDirection = 'asc' | 'desc'

export type MenuState =
  | {
      kind: 'file'
      item: JobItem
      x: number
      y: number
    }
  | {
      kind: 'folder'
      item: FolderNode
      x: number
      y: number
    }
  | {
      kind: 'surface'
      x: number
      y: number
    }

export type VisibleEntry =
  | { key: string; kind: 'folder'; item: FolderNode }
  | { key: string; kind: 'file'; item: JobItem }

export type FilterMenu = 'type' | 'date' | null

export type SelectionBox = {
  startX: number
  startY: number
  currentX: number
  currentY: number
}

export type DragState = {
  jobIds: string[]
  folderIds: string[]
}

export type PendingUpload = {
  localId: string
  clientUploadId: string
  jobId?: string
  folderId: string
  filename: string
  stage: 'uploading' | 'queued' | 'processing' | 'failed'
  progress: number
}

export type FileListJob = JobItem & {
  __pending?: boolean
  __jobId?: string
}

export type TextDialogState =
  | {
      kind: 'create-folder'
      title: string
      label: string
      submitLabel: string
      value: string
      parentId: string
      after: 'refresh' | 'move-browser'
    }
  | {
      kind: 'rename-folder'
      title: string
      label: string
      submitLabel: string
      value: string
      folderId: string
    }
  | {
      kind: 'rename-file'
      title: string
      label: string
      submitLabel: string
      value: string
      jobId: string
    }

export type ConfirmDialogState =
  | {
      kind: 'delete-file'
      title: string
      body: string
      item: JobItem
    }
  | {
      kind: 'delete-folder'
      title: string
      body: string
      item: FolderNode
    }

export const TYPE_OPTIONS: Array<{ value: TypeFilter; label: string }> = [
  { value: 'folder', label: '폴더' },
  { value: 'document', label: '문서' },
]

export const DATE_OPTIONS: Array<{ value: DateFilter; label: string }> = [
  { value: 'past_hour', label: '지난 1시간' },
  { value: 'today', label: '오늘' },
  { value: 'past_7_days', label: '지난 7일' },
  { value: 'past_30_days', label: '지난 30일' },
  { value: 'this_year', label: '올해' },
  { value: 'last_year', label: '지난 해' },
]
