import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import type { ChangeEvent, DragEvent as ReactDragEvent, FormEvent, MouseEvent as ReactMouseEvent } from 'react'

import { batchDownloadJobs, createFolder, downloadFolder, fetchFiles, moveEntries, renameFolder, renameJob, trashFolder, trashJob } from './api'
import type { FilesResponse, FolderNode, JobItem } from './types'
import {
  DATE_OPTIONS,
  TYPE_OPTIONS,
  type ConfirmDialogState,
  type DateFilter,
  type DragState,
  type FileListJob,
  type FilesPageProps,
  type FilterMenu,
  type MenuState,
  type MoveState,
  type SelectionBox,
  type SortDirection,
  type SortKey,
  type UploadBatchState,
  type UploadState,
  type TextDialogState,
  type TypeFilter,
  type VisibleEntry,
} from './filesPageTypes'
import {
  buildMovePath,
  clamp,
  clampMenu,
  currentFolderName,
  defaultSortState,
  displayFilename,
  entryKey,
  fileIconForJob,
  fileTypeLabel,
  formatBytes,
  formatJobSub,
  formatPendingStatus,
  matchesFolderFilters,
  matchesFolderTypeFilter,
  matchesJobFilters,
  matchesJobTypeFilter,
  normalizePage,
  normalizeRect,
  readDragPayload,
  rectanglesIntersect,
  renderSortMark,
  selectionBoxStyle,
  sortEntries,
  sortJobs,
  stripExtension,
  typeFilterLabel,
  updateQuery,
} from './filesPageUtils'
import { dateFilterLabel, extractDate, formatCompactDate } from './filesPageDateUtils'
import { enqueuePendingUploads, matchesPendingUpload, reconcilePendingUploads, startPendingUpload, usePendingUploads } from './uploadStore'
import { usePageTitle } from '../../usePageTitle'

function detectUploadFileType(file: File): UploadState['fileType'] | null {
  const name = file.name.toLowerCase()
  if (name.endsWith('.pdf') || file.type === 'application/pdf') {
    return 'pdf'
  }
  if (file.type.startsWith('audio/') || /\.(mp3|wav|m4a)$/i.test(file.name)) {
    return 'audio'
  }
  return null
}

function buildUploadItem(file: File, folderId: string): UploadState | null {
  const fileType = detectUploadFileType(file)
  if (!fileType) {
    return null
  }
  return {
    file,
    displayName: '',
    folderId,
    description: '',
    refineEnabled: fileType !== 'pdf',
    fileType,
  }
}

function hasExternalFiles(dataTransfer: DataTransfer) {
  return Array.from(dataTransfer.types).includes('Files')
}

function shouldShowPendingSpinner(job: FileListJob) {
  if (job.__pending) {
    return job.Status !== '업로드 실패'
  }
  const statusCode = job.StatusCode ?? 0
  return statusCode === 10 || statusCode === 20 || statusCode === 30 || statusCode === 40
}

function renderJobSub(job: FileListJob) {
  return (
    <span className="drive-item-sub">
      {shouldShowPendingSpinner(job) ? <span aria-hidden="true" className="inline-progress-spinner" /> : null}
      <span className="drive-item-sub-text">{formatJobSub(job)}</span>
    </span>
  )
}

function renderMobileStatusMeta(job: FileListJob) {
  return <span className="drive-table-meta mobile-status-meta">{formatJobSub(job)}</span>
}

function renderMobileTypeMeta(job: FileListJob) {
  return <span className="drive-table-meta mobile-type-meta">{fileTypeLabel(job.FileType)}</span>
}

function renderMobileJobMeta(job: FileListJob) {
  return (
    <div className="drive-table-mobile-meta">
      {renderMobileStatusMeta(job)}
      {renderMobileTypeMeta(job)}
      <span className="drive-table-meta mobile-date-meta">{formatCompactDate(job.UpdatedAt)}</span>
    </div>
  )
}

function renderMobileHomeJobMeta(job: FileListJob) {
  return (
    <div className="drive-table-mobile-meta">
      {renderMobileStatusMeta(job)}
      {renderMobileTypeMeta(job)}
      <span className="drive-table-meta mobile-location-meta">{job.FolderName || '내 파일'}</span>
      <span className="drive-table-meta mobile-date-meta">{formatCompactDate(job.UpdatedAt)}</span>
    </div>
  )
}

export function FilesPage({ viewMode }: FilesPageProps) {
  const navigate = useNavigate()
  const params = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const [data, setData] = useState<FilesResponse | null>(null)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [reloadToken, setReloadToken] = useState(0)
  const [moveState, setMoveState] = useState<MoveState | null>(null)
  const [moveTargetId, setMoveTargetId] = useState('')
  const [moveBrowserFolderId, setMoveBrowserFolderId] = useState('')
  const [moveBrowserData, setMoveBrowserData] = useState<FilesResponse | null>(null)
  const [uploadBatchState, setUploadBatchState] = useState<UploadBatchState | null>(null)
  const [menuState, setMenuState] = useState<MenuState | null>(null)
  const [selectedKeys, setSelectedKeys] = useState<string[]>([])
  const [selectionAnchor, setSelectionAnchor] = useState<string | null>(null)
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all')
  const [dateFilter, setDateFilter] = useState<DateFilter>('all')
  const [sortKey, setSortKey] = useState<SortKey>(() => defaultSortState(viewMode).key)
  const [sortDirection, setSortDirection] = useState<SortDirection>(() => defaultSortState(viewMode).direction)
  const [filterMenu, setFilterMenu] = useState<FilterMenu>(null)
  const [selectionBox, setSelectionBox] = useState<SelectionBox | null>(null)
  const [dragState, setDragState] = useState<DragState | null>(null)
  const [isUploadDragActive, setIsUploadDragActive] = useState(false)
  const [dropTargetFolderId, setDropTargetFolderId] = useState('')
  const [textDialog, setTextDialog] = useState<TextDialogState | null>(null)
  const [confirmDialog, setConfirmDialog] = useState<ConfirmDialogState | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const driveTableRef = useRef<HTMLDivElement | null>(null)
  const rowRefs = useRef<Record<string, HTMLDivElement | null>>({})

  const folderId = viewMode === 'explore' ? params.folderId ?? '' : ''
  const query = searchParams.get('q') ?? ''
  const page = normalizePage(searchParams.get('page'))
  const pendingUploads = usePendingUploads()
  const folderTitle = data?.folder_path?.[data.folder_path.length - 1]?.Name || ''
  const uploadItems = uploadBatchState?.items ?? []
  const isSingleUpload = uploadItems.length === 1
  const pdfMaxPages = data?.upload_limits?.pdf_max_pages ?? 0
  const pdfMaxPagesPerRequest = data?.upload_limits?.pdf_max_pages_per_request ?? 0

  usePageTitle(viewMode === 'home' ? 'Home' : viewMode === 'search' ? 'Search' : folderId ? folderTitle || 'Folder' : 'My Files')

  useEffect(() => {
    const openPicker = () => {
      fileInputRef.current?.click()
    }
    window.addEventListener('whisper:new-file', openPicker as EventListener)
    return () => {
      window.removeEventListener('whisper:new-file', openPicker as EventListener)
    }
  }, [])

  useEffect(() => {
    let closed = false
    const controller = new AbortController()

    async function load() {
      try {
        const payload = await fetchFiles({
          viewMode,
          folderId,
          query,
          tag: '',
          sort: 'updated',
          order: 'desc',
          page,
          signal: controller.signal,
        })
        if (!closed && payload) {
          setData(payload)
          reconcilePendingUploads(payload.job_items)
          setError('')
        }
      } catch (loadError) {
        if (!closed && !controller.signal.aborted) {
          setError(normalizeLoadError(loadError, '목록을 불러오지 못했습니다.'))
        }
      }
    }

    void load()
    return () => {
      closed = true
      controller.abort()
    }
  }, [viewMode, folderId, query, page, reloadToken])

  useEffect(() => {
    if (!moveState) {
      return
    }

    let closed = false
    const controller = new AbortController()

    async function loadMoveBrowser() {
      try {
        const payload = await fetchFiles({
          viewMode: 'explore',
          folderId: moveBrowserFolderId,
          query: '',
          tag: '',
          sort: 'updated',
          order: 'desc',
          page: 1,
          signal: controller.signal,
        })
        if (!closed && payload) {
          setMoveBrowserData(payload)
        }
      } catch (loadError) {
        if (!closed && !controller.signal.aborted) {
          setError(normalizeLoadError(loadError, '이동 위치를 불러오지 못했습니다.'))
        }
      }
    }

    void loadMoveBrowser()
    return () => {
      closed = true
      controller.abort()
    }
  }, [moveBrowserFolderId, moveState])

  useEffect(() => {
    const source = new EventSource('/api/events')
    let timer = 0

    const scheduleRefresh = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => {
        setReloadToken((value) => value + 1)
      }, 120)
    }

    source.addEventListener('update', scheduleRefresh)
    source.onerror = () => {
      // EventSource reconnects automatically.
    }

    return () => {
      window.clearTimeout(timer)
      source.removeEventListener('update', scheduleRefresh)
      source.close()
    }
  }, [])

  useEffect(() => {
    if (!menuState) {
      return
    }

    const close = () => setMenuState(null)
    window.addEventListener('click', close)
    window.addEventListener('scroll', close, true)
    window.addEventListener('resize', close)
    return () => {
      window.removeEventListener('click', close)
      window.removeEventListener('scroll', close, true)
      window.removeEventListener('resize', close)
    }
  }, [menuState])

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

  const allFolders = useMemo(() => data?.all_folders ?? [], [data?.all_folders])
  const folderItems = useMemo(() => data?.folder_items ?? [], [data?.folder_items])
  const jobItems = useMemo(() => data?.job_items ?? [], [data?.job_items])
  const folderPath = data?.folder_path ?? []
  const filteredFolderItems = useMemo(
    () => folderItems.filter((folder) => matchesFolderFilters(folder, dateFilter) && matchesFolderTypeFilter(typeFilter)),
    [dateFilter, folderItems, typeFilter],
  )
  const filteredJobItems = useMemo(
    () => jobItems.filter((job) => matchesJobFilters(job, dateFilter) && matchesJobTypeFilter(job, typeFilter)),
    [dateFilter, jobItems, typeFilter],
  )
  const showFolderSections = matchesFolderTypeFilter(typeFilter)
  const showJobSections = typeFilter !== 'folder'
  const pendingVisibleJobs = useMemo(
    () =>
      pendingUploads
        .filter((item) => viewMode === 'home' || item.folderId === folderId)
        .filter((item) => dateFilter === 'all' && matchesJobTypeFilter({ FileType: item.fileType }, typeFilter))
        .map((item) => ({
          ID: item.jobId || item.localId,
          Filename: item.filename,
          FileType: item.fileType,
          MediaDuration: '',
          SizeBytes: 0,
          Status:
            item.stage === 'failed'
              ? '업로드 실패'
              : formatPendingStatus(item.stage, item.progress),
          Phase: item.stage === 'queued' ? '대기 중' : item.stage === 'converting' ? '파일을 변환하는 중' : '',
          ProgressPercent: item.progress,
          IsRefined: false,
          TagText: '',
          FolderID: item.folderId,
          ClientUploadID: item.clientUploadId,
          UpdatedAt: '',
          FolderName: currentFolderName(item.folderId, allFolders),
          __pending: true,
          __jobId: item.jobId || '',
        })),
    [allFolders, dateFilter, folderId, pendingUploads, typeFilter, viewMode],
  )
  const actualJobItems = useMemo(
    () => jobItems.filter((job) => !pendingUploads.some((item) => matchesPendingUpload(job, item))),
    [jobItems, pendingUploads],
  )
  const filteredActualJobItems = useMemo(
    () => actualJobItems.filter((job) => matchesJobFilters(job, dateFilter) && matchesJobTypeFilter(job, typeFilter)),
    [actualJobItems, dateFilter, typeFilter],
  )
  const sortedHomeJobs = useMemo(
    () => sortJobs(filteredActualJobItems, sortKey === 'kind' ? 'updated' : sortKey, sortDirection),
    [filteredActualJobItems, sortDirection, sortKey],
  )
  const homeDisplayJobs = useMemo(() => [...pendingVisibleJobs, ...sortedHomeJobs], [pendingVisibleJobs, sortedHomeJobs])
  const homeVisibleEntries = useMemo<VisibleEntry[]>(
    () => [
      ...(showFolderSections ? filteredFolderItems.map((folder) => ({ key: entryKey('folder', folder.ID), kind: 'folder' as const, item: folder })) : []),
      ...(showJobSections ? homeDisplayJobs.map((job) => ({ key: entryKey('file', job.ID), kind: 'file' as const, item: job })) : []),
    ],
    [filteredFolderItems, homeDisplayJobs, showFolderSections, showJobSections],
  )
  const sortedExploreEntries = useMemo<VisibleEntry[]>(
    () =>
      sortEntries(
        [
          ...filteredFolderItems.map((folder) => ({ key: entryKey('folder', folder.ID), kind: 'folder' as const, item: folder })),
          ...[...pendingVisibleJobs, ...filteredJobItems.filter((job) => !pendingUploads.some((item) => matchesPendingUpload(job, item)))].map((job) => ({
            key: entryKey('file', job.ID),
            kind: 'file' as const,
            item: job,
          })),
        ],
        sortKey,
        sortDirection,
      ),
    [filteredFolderItems, filteredJobItems, pendingUploads, pendingVisibleJobs, sortDirection, sortKey],
  )
  const visibleEntries = useMemo<VisibleEntry[]>(
    () => (viewMode === 'explore' ? sortedExploreEntries : viewMode === 'home' ? homeVisibleEntries : []),
    [homeVisibleEntries, sortedExploreEntries, viewMode],
  )

  useEffect(() => {
    if ((viewMode !== 'explore' && viewMode !== 'home') || uploadBatchState || textDialog || confirmDialog || moveState) {
      return
    }

    const handleSelectAll = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'a' || event.altKey) {
        return
      }
      const target = event.target as HTMLElement | null
      if (target?.closest('input, textarea, select, [contenteditable="true"]') || visibleEntries.length === 0) {
        return
      }
      event.preventDefault()
      const nextKeys = visibleEntries.map((entry) => entry.key)
      setSelectedKeys(nextKeys)
      setSelectionAnchor(nextKeys[0] ?? null)
      setMenuState(null)
    }

    window.addEventListener('keydown', handleSelectAll)
    return () => {
      window.removeEventListener('keydown', handleSelectAll)
    }
  }, [confirmDialog, moveState, textDialog, uploadBatchState, viewMode, visibleEntries])

  const selectedEntries = useMemo(
    () => visibleEntries.filter((entry) => selectedKeys.includes(entry.key)),
    [selectedKeys, visibleEntries],
  )
  const selectedJobIds = selectedEntries.filter((entry) => entry.kind === 'file').map((entry) => entry.item.ID)
  const selectedFolderIds = selectedEntries.filter((entry) => entry.kind === 'folder').map((entry) => entry.item.ID)
  const moveSelectionFolderIds = useMemo(
    () => (moveState?.id === '__selection__' ? selectedFolderIds : moveState?.type === 'folder' ? [moveState.id] : []),
    [moveState, selectedFolderIds],
  )
  const moveSelectionJobIds = useMemo(
    () => (moveState?.id === '__selection__' ? selectedJobIds : moveState?.type === 'file' ? [moveState.id] : []),
    [moveState, selectedJobIds],
  )
  const moveFolderChildren = useMemo(
    () => (moveBrowserData?.folder_items ?? []).filter((folder) => !moveSelectionFolderIds.includes(folder.ID)),
    [moveBrowserData?.folder_items, moveSelectionFolderIds],
  )
  const moveFileChildren = useMemo(
    () => (moveBrowserData?.job_items ?? []).filter((job) => !moveSelectionJobIds.includes(job.ID)),
    [moveBrowserData?.job_items, moveSelectionJobIds],
  )

  useEffect(() => {
    if (!selectionBox || viewMode !== 'explore') {
      return
    }

    const handleMouseMove = (event: MouseEvent) => {
      const table = driveTableRef.current
      if (!table) {
        return
      }
      const bounds = table.getBoundingClientRect()
      const nextBox = {
        startX: selectionBox.startX,
        startY: selectionBox.startY,
        currentX: clamp(event.clientX - bounds.left, 0, bounds.width),
        currentY: clamp(event.clientY - bounds.top, 0, bounds.height),
      }
      setSelectionBox(nextBox)

      const relativeRect = normalizeRect(nextBox)
      const selectionRect = {
        left: bounds.left + relativeRect.left,
        top: bounds.top + relativeRect.top,
        right: bounds.left + relativeRect.right,
        bottom: bounds.top + relativeRect.bottom,
      }
      const nextSelectedKeys = visibleEntries
        .filter((entry) => {
          const node = rowRefs.current[entry.key]
          if (!node) {
            return false
          }
          const rect = node.getBoundingClientRect()
          return rectanglesIntersect(selectionRect, rect)
        })
        .map((entry) => entry.key)

      setSelectedKeys(nextSelectedKeys)
    }

    const handleMouseUp = () => {
      setSelectionBox(null)
    }

    window.addEventListener('mousemove', handleMouseMove)
    window.addEventListener('mouseup', handleMouseUp)
    return () => {
      window.removeEventListener('mousemove', handleMouseMove)
      window.removeEventListener('mouseup', handleMouseUp)
    }
  }, [selectionBox, viewMode, visibleEntries])

  const refresh = () => {
    setMenuState(null)
    setSelectedKeys([])
    setSelectionAnchor(null)
    setReloadToken((value) => value + 1)
  }

  const openUploadBatch = (files: FileList | File[], targetFolderId: string) => {
    const items = Array.from(files)
      .map((file) => buildUploadItem(file, targetFolderId))
      .filter((item): item is UploadState => Boolean(item))
    if (items.length === 0) {
      setError('업로드할 수 있는 오디오 또는 PDF 파일이 없습니다.')
      return
    }
    if (items.length < Array.from(files).length) {
      setMessage('지원하지 않는 파일 형식은 제외했습니다.')
    }
    setUploadBatchState({
      folderId: targetFolderId,
      descriptionMode: 'shared',
      sharedDescription: '',
      items,
    })
  }

  const handleUploadFileInput = (event: ChangeEvent<HTMLInputElement>) => {
    const files = event.target.files
    if (!files || files.length === 0) {
      return
    }
    openUploadBatch(files, folderId)
    event.target.value = ''
  }

  const handleUploadSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!uploadBatchState || uploadBatchState.items.length === 0) {
      return
    }
    const items =
      uploadBatchState.descriptionMode === 'shared'
        ? uploadBatchState.items.map((item) => ({ ...item, description: uploadBatchState.sharedDescription }))
        : uploadBatchState.items
    try {
      setUploadBatchState(null)
      setMessage(items.length > 1 ? `${items.length}개 파일 업로드를 시작했습니다.` : '업로드를 시작했습니다.')
      const pendingItems = enqueuePendingUploads(items)
      let failedCount = 0
      for (let index = 0; index < items.length; index += 1) {
        try {
          await startPendingUpload(items[index], pendingItems[index])
        } catch {
          failedCount += 1
        }
      }
      if (failedCount > 0) {
        setError(`${failedCount}개 파일 업로드에 실패했습니다.`)
      }
      setReloadToken((value) => value + 1)
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : '업로드에 실패했습니다.')
    }
  }

  const canDropUploadFiles = viewMode === 'home' || viewMode === 'explore'
  const uploadDropFolderId = viewMode === 'explore' ? folderId : ''

  const handleUploadDragOver = (event: ReactDragEvent<HTMLElement>) => {
    if (!canDropUploadFiles || !hasExternalFiles(event.dataTransfer)) {
      return
    }
    event.preventDefault()
    event.dataTransfer.dropEffect = 'copy'
    setIsUploadDragActive(true)
  }

  const handleUploadDragLeave = (event: ReactDragEvent<HTMLElement>) => {
    const nextTarget = event.relatedTarget as Node | null
    if (nextTarget && event.currentTarget.contains(nextTarget)) {
      return
    }
    setIsUploadDragActive(false)
  }

  const handleUploadDrop = (event: ReactDragEvent<HTMLElement>) => {
    if (!canDropUploadFiles || !hasExternalFiles(event.dataTransfer)) {
      return
    }
    event.preventDefault()
    setIsUploadDragActive(false)
    openUploadBatch(event.dataTransfer.files, uploadDropFolderId)
  }

  const openCreateFolderDialog = (parentId: string, after: 'refresh' | 'move-browser' = 'refresh') => {
    setTextDialog({
      kind: 'create-folder',
      title: '새 폴더',
      label: '폴더 이름',
      submitLabel: '생성',
      value: '',
      parentId,
      after,
    })
  }

  const handleCreateFolder = async (name: string, parentId: string, after: 'refresh' | 'move-browser') => {
    try {
      await createFolder(name.trim(), parentId)
      setMessage('폴더를 만들었습니다.')
      setTextDialog(null)
      if (after === 'move-browser') {
        setReloadToken((value) => value + 1)
        setMoveTargetId(parentId)
        setMoveBrowserFolderId(parentId)
        return
      }
      refresh()
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '폴더를 만들지 못했습니다.')
    }
  }

  const handleMove = async () => {
    if (!moveState) {
      return
    }
    try {
      if (moveState.id === '__selection__') {
        await moveEntries(selectedJobIds, selectedFolderIds, moveTargetId)
      } else {
        await moveEntries(moveState.type === 'file' ? [moveState.id] : [], moveState.type === 'folder' ? [moveState.id] : [], moveTargetId)
      }
      setMessage('항목을 이동했습니다.')
      setMoveState(null)
      setMoveTargetId('')
      setMoveBrowserFolderId('')
      refresh()
    } catch (moveError) {
      setError(moveError instanceof Error ? moveError.message : '이동에 실패했습니다.')
    }
  }

  const handleBulkMove = async () => {
    if (selectedEntries.length === 0) {
      return
    }
    setMoveState({
      type: 'folder',
      id: '__selection__',
      name: `${selectedEntries.length}개 항목`,
    })
    setMoveTargetId(folderId)
    setMoveBrowserFolderId(folderId)
  }

  const handleBulkDelete = async () => {
    if (selectedEntries.length === 0) {
      return
    }
    if (selectedFolderIds.length > 0 && !window.confirm(`${selectedFolderIds.length}개 폴더를 완전삭제할까요? 폴더 안의 파일도 함께 완전삭제됩니다.`)) {
      return
    }
    try {
      for (const id of selectedFolderIds) {
        await trashFolder(id)
      }
      for (const id of selectedJobIds) {
        await trashJob(id)
      }
      setMessage(selectedFolderIds.length > 0 ? `${selectedEntries.length}개 항목을 삭제했습니다.` : `${selectedEntries.length}개 항목을 휴지통으로 보냈습니다.`)
      refresh()
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '선택 항목 삭제에 실패했습니다.')
    }
  }

  const handleBulkDownload = async () => {
    if (selectedEntries.length === 0) {
      return
    }
    selectedFolderIds.forEach((id) => {
      void downloadFolder(id)
    })
    batchDownloadJobs(selectedJobIds)
  }

  const handleFileAction = async (action: 'move' | 'delete' | 'download', item: JobItem) => {
    try {
      if (action === 'move') {
        setMoveState({ type: 'file', id: item.ID, name: item.Filename })
        setMoveTargetId(folderId || item.FolderID || '')
        setMoveBrowserFolderId(folderId || item.FolderID || '')
        return
      }
      if (action === 'delete') {
        setConfirmDialog({
          kind: 'delete-file',
          title: '파일 삭제',
          body: `"${displayFilename(item.Filename)}" 파일을 휴지통으로 보낼까요?`,
          item,
        })
        return
      }
      window.location.href = item.IsRefined ? `/download/${item.ID}/refined` : `/download/${item.ID}`
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '파일 작업에 실패했습니다.')
    }
  }

  const handleFolderAction = async (action: 'move' | 'delete' | 'download', item: FolderNode) => {
    try {
      if (action === 'move') {
        setMoveState({ type: 'folder', id: item.ID, name: item.Name })
        setMoveTargetId(folderId || item.ParentID || '')
        setMoveBrowserFolderId(folderId || item.ParentID || '')
        return
      }
      if (action === 'delete') {
        setConfirmDialog({
          kind: 'delete-folder',
          title: '폴더 삭제',
          body: `"${item.Name}" 폴더를 완전삭제할까요? 폴더 안의 파일도 함께 완전삭제됩니다.`,
          item,
        })
        return
      }
      await downloadFolder(item.ID)
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '폴더 작업에 실패했습니다.')
    }
  }

  const openFileMenu = (item: JobItem, event: ReactMouseEvent<HTMLElement>) => {
    event.preventDefault()
    event.stopPropagation()
    syncMenuSelection(entryKey('file', item.ID))
    setMenuState({ kind: 'file', item, x: event.clientX, y: event.clientY })
  }

  const openFolderMenu = (item: FolderNode, event: ReactMouseEvent<HTMLElement>) => {
    event.preventDefault()
    event.stopPropagation()
    syncMenuSelection(entryKey('folder', item.ID))
    setMenuState({ kind: 'folder', item, x: event.clientX, y: event.clientY })
  }

  const openSurfaceMenu = (event: ReactMouseEvent<HTMLElement>) => {
    if (viewMode !== 'explore') {
      return
    }
    const target = event.target as HTMLElement
    if (
      target.closest('.drive-table-row') ||
      target.closest('.drive-table-header') ||
      target.closest('.row-menu-button') ||
      target.closest('.filter-toolbar') ||
      target.closest('.selection-toolbar') ||
      target.closest('.drive-pathbar')
    ) {
      return
    }
    event.preventDefault()
    clearSelection()
    setMenuState({ kind: 'surface', x: event.clientX, y: event.clientY })
  }

  const syncMenuSelection = (key: string) => {
    const hasKey = selectedKeys.includes(key)
    setSelectedKeys((current) => {
      if (current.includes(key)) {
        return current
      }
      return [key]
    })
    setSelectionAnchor((current) => (hasKey ? current : key))
  }

  const handleEntryClick = (key: string, event: ReactMouseEvent<HTMLElement>) => {
    setMenuState(null)
    if (event.shiftKey && selectionAnchor) {
      const anchorIndex = visibleEntries.findIndex((entry) => entry.key === selectionAnchor)
      const targetIndex = visibleEntries.findIndex((entry) => entry.key === key)
      if (anchorIndex >= 0 && targetIndex >= 0) {
        const start = Math.min(anchorIndex, targetIndex)
        const end = Math.max(anchorIndex, targetIndex)
        setSelectedKeys(visibleEntries.slice(start, end + 1).map((entry) => entry.key))
        return
      }
    }

    if (event.metaKey || event.ctrlKey) {
      setSelectedKeys((current) => (current.includes(key) ? current.filter((entry) => entry !== key) : [...current, key]))
      setSelectionAnchor(key)
      return
    }

    setSelectedKeys([key])
    setSelectionAnchor(key)
  }

  const openFolder = (id: string) => navigate(`/files/folder/${id}`)
  const openFile = (id: string) => navigate(`/file/${id}`)
  const handleRenameFromMenu = async () => {
    if (!menuState || menuState.kind === 'surface' || selectedKeys.length !== 1) {
      return
    }
    if (menuState.kind === 'folder') {
      setTextDialog({
        kind: 'rename-folder',
        title: '폴더 이름 변경',
        label: '새 이름',
        submitLabel: '변경',
        value: menuState.item.Name,
        folderId: menuState.item.ID,
      })
      return
    }
    setTextDialog({
      kind: 'rename-file',
      title: '파일 이름 변경',
      label: '새 이름',
      submitLabel: '변경',
      value: menuState.item.Filename,
      jobId: menuState.item.ID,
    })
  }
  const clearSelection = () => {
    setSelectedKeys([])
    setSelectionAnchor(null)
    setMenuState(null)
  }

  const beginSelectionBox = (event: ReactMouseEvent<HTMLDivElement>) => {
    if (viewMode !== 'explore' || event.button !== 0) {
      return
    }
    const target = event.target as HTMLElement
    if (
      target.closest('.drive-table-row') ||
      target.closest('.drive-table-header') ||
      target.closest('.row-menu-button') ||
      target.closest('.filter-toolbar') ||
      target.closest('.selection-toolbar') ||
      target.closest('.drive-pathbar')
    ) {
      return
    }
    const table = driveTableRef.current
    if (!table) {
      return
    }
    const bounds = table.getBoundingClientRect()
    const startX = event.clientX - bounds.left
    const startY = event.clientY - bounds.top
    setMenuState(null)
    setSelectionAnchor(null)
    setSelectedKeys([])
    setSelectionBox({ startX, startY, currentX: startX, currentY: startY })
    event.preventDefault()
  }

  const getDraggedEntryIds = (key: string) => {
    const activeKeys = selectedKeys.includes(key) ? selectedKeys : [key]
    const entries = visibleEntries.filter((entry) => activeKeys.includes(entry.key))
    return {
      jobIds: entries.filter((entry) => entry.kind === 'file').map((entry) => entry.item.ID),
      folderIds: entries.filter((entry) => entry.kind === 'folder').map((entry) => entry.item.ID),
    }
  }

  const handleRowDragStart = (key: string, event: ReactDragEvent<HTMLDivElement>) => {
    if (viewMode !== 'explore') {
      return
    }
    const payload = getDraggedEntryIds(key)
    setDragState(payload)
    if (!selectedKeys.includes(key)) {
      setSelectedKeys([key])
      setSelectionAnchor(key)
    }
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('application/x-whisper-entries', JSON.stringify(payload))
    event.dataTransfer.setData('text/plain', key)
  }

  const handleFolderDragOver = (folderId: string, event: ReactDragEvent<HTMLDivElement>) => {
    if (hasExternalFiles(event.dataTransfer)) {
      return
    }
    const payload = readDragPayload(event.dataTransfer) ?? dragState
    if (!payload || payload.folderIds.includes(folderId)) {
      return
    }
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
    setDropTargetFolderId(folderId)
  }

  const handleFolderDragLeave = (folderId: string, event: ReactDragEvent<HTMLDivElement>) => {
    const nextTarget = event.relatedTarget as Node | null
    if (nextTarget && event.currentTarget.contains(nextTarget)) {
      return
    }
    setDropTargetFolderId((current) => (current === folderId ? '' : current))
  }

  const handleFolderDrop = async (folderId: string, event: ReactDragEvent<HTMLDivElement>) => {
    if (hasExternalFiles(event.dataTransfer)) {
      return
    }
    event.preventDefault()
    const payload = readDragPayload(event.dataTransfer) ?? dragState
    if (!payload || payload.folderIds.includes(folderId)) {
      setDropTargetFolderId('')
      setDragState(null)
      return
    }
    try {
      await moveEntries(payload.jobIds, payload.folderIds, folderId)
      setMessage('항목을 이동했습니다.')
      setDropTargetFolderId('')
      setDragState(null)
      refresh()
    } catch (moveError) {
      setError(moveError instanceof Error ? moveError.message : '이동에 실패했습니다.')
      setDropTargetFolderId('')
      setDragState(null)
    }
  }

  const handleRowDragEnd = () => {
    setDropTargetFolderId('')
    setDragState(null)
  }

  const toggleSort = (nextKey: SortKey) => {
    if (sortKey === nextKey) {
      setSortDirection((current) => (current === 'desc' ? 'asc' : 'desc'))
      return
    }
    setSortKey(nextKey)
    setSortDirection(nextKey === 'updated' ? 'desc' : 'asc')
  }

  const resetFilters = () => {
    setTypeFilter('all')
    setDateFilter('all')
    setFilterMenu(null)
  }

  const hasActiveFilters = typeFilter !== 'all' || dateFilter !== 'all'
  const moveCurrentPath = buildMovePath(moveBrowserData?.all_folders ?? allFolders, moveBrowserFolderId)
  const moveTitle = moveState
    ? moveState.id === '__selection__'
      ? `${selectedEntries.length}개 항목 이동중`
      : `"${displayFilename(moveState.name)}" 이동중`
    : ''
  const renderFilterToolbar = () => (
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
      {hasActiveFilters ? (
        <div className="filter-group">
          <button className="filter-reset-button" onClick={resetFilters} type="button">
            필터 지우기
          </button>
        </div>
      ) : null}
    </div>
  )

  const updateUploadItem = (index: number, updates: Partial<UploadState>) => {
    setUploadBatchState((current) => {
      if (!current) {
        return current
      }
      return {
        ...current,
        items: current.items.map((item, itemIndex) => (itemIndex === index ? { ...item, ...updates } : item)),
      }
    })
  }

  const submitTextDialog = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!textDialog || !textDialog.value.trim()) {
      return
    }
    try {
      if (textDialog.kind === 'create-folder') {
        await handleCreateFolder(textDialog.value, textDialog.parentId, textDialog.after)
        return
      }
      if (textDialog.kind === 'rename-folder') {
        await renameFolder(textDialog.folderId, textDialog.value.trim())
      } else {
        await renameJob(textDialog.jobId, textDialog.value.trim())
      }
      setMessage('이름을 변경했습니다.')
      setTextDialog(null)
      setMenuState(null)
      refresh()
    } catch (renameError) {
      setError(renameError instanceof Error ? renameError.message : textDialog.kind === 'create-folder' ? '폴더를 만들지 못했습니다.' : '이름 변경에 실패했습니다.')
    }
  }

  const handleConfirmDelete = async () => {
    if (!confirmDialog) {
      return
    }
    try {
      if (confirmDialog.kind === 'delete-file') {
        await trashJob(confirmDialog.item.ID)
        setMessage('파일을 휴지통으로 보냈습니다.')
      } else {
        await trashFolder(confirmDialog.item.ID)
        setMessage('폴더를 삭제했습니다.')
      }
      setConfirmDialog(null)
      refresh()
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '삭제에 실패했습니다.')
    }
  }

  return (
    <div className="view-shell">
      <input hidden accept=".mp3,.wav,.m4a,.pdf,audio/*,application/pdf" multiple onChange={handleUploadFileInput} ref={fileInputRef} type="file" />

      <section
        className={`content-surface${isUploadDragActive ? ' upload-drop-active' : ''}`}
        onDragLeave={handleUploadDragLeave}
        onDragOver={handleUploadDragOver}
        onDrop={handleUploadDrop}
        onClick={(event) => {
          if (event.target === event.currentTarget) {
            clearSelection()
          }
        }}
      >
        {!data ? <div className="empty-panel">목록을 불러오는 중입니다.</div> : null}

        {data && viewMode === 'home' ? (
          <div className="drive-sections">
            {selectedEntries.length > 0 ? (
              <div className="selection-toolbar">
                <div className="selection-toolbar-inner">
                  <div className="selection-toolbar-main">
                    <button className="selection-toolbar-close" onClick={() => clearSelection()} type="button">
                      ×
                    </button>
                    <div className="selection-toolbar-copy">{selectedEntries.length}개 선택됨</div>
                  </div>
                  <div className="selection-toolbar-actions">
                    <button className="toolbar-button" onClick={() => void handleBulkDownload()} type="button">
                      다운로드
                    </button>
                    <button className="toolbar-button" onClick={() => void handleBulkMove()} type="button">
                      이동
                    </button>
                    <button className="toolbar-button danger" onClick={() => void handleBulkDelete()} type="button">
                      삭제
                    </button>
                  </div>
                </div>
              </div>
            ) : (
              <div className="home-selection-slot" />
            )}
            {showFolderSections ? (
            <section className="drive-section">
              <div className="drive-section-header">
                <h2>최근 수정 폴더</h2>
              </div>
              <div className="drive-folder-strip">
                {filteredFolderItems.map((folder) => {
                  const key = entryKey('folder', folder.ID)
                  return (
                  <article
                    className={`drive-folder-card${selectedKeys.includes(key) ? ' selected' : ''}`}
                    key={folder.ID}
                    onContextMenu={(event) => openFolderMenu(folder, event)}
                    onClick={(event) => handleEntryClick(key, event)}
                    onDoubleClick={() => openFolder(folder.ID)}
                  >
                    <div className="drive-folder-card-main" role="button" tabIndex={0}>
                      <span className="drive-item-icon">📁</span>
                      <span className="drive-folder-card-copy">
                        <strong>{folder.Name}</strong>
                        <span>위치: {folder.ParentID ? '하위 폴더' : '내 파일'}</span>
                      </span>
                    </div>
                    <button
                      aria-label={`${folder.Name} 작업 열기`}
                      className="row-menu-button"
                      onClick={(event) => openFolderMenu(folder, event)}
                      type="button"
                    >
                      ⋮
                    </button>
                  </article>
                  )
                })}
              </div>
            </section>
            ) : null}

            {showJobSections ? (
            <section className="drive-section">
              <div className="drive-section-header">
                <h2>최근 수정 파일</h2>
              </div>
              <div className="drive-table">
                <div className="drive-table-header home">
                  <button className="column-sort-button" onClick={() => toggleSort('name')} type="button">
                    이름{renderSortMark(sortKey, sortDirection, 'name')}
                  </button>
                  <span>종류</span>
                  <button className="column-sort-button" onClick={() => toggleSort('updated')} type="button">
                    수정 날짜{renderSortMark(sortKey, sortDirection, 'updated')}
                  </button>
                  <button className="column-sort-button" onClick={() => toggleSort('location')} type="button">
                    위치{renderSortMark(sortKey, sortDirection, 'location')}
                  </button>
                  <span className="drive-table-menu-col" />
                </div>
                {homeDisplayJobs.map((job) => {
                  const key = entryKey('file', job.ID)
                  const isPending = Boolean((job as FileListJob).__pending)
                  const canOpen = !isPending || Boolean((job as FileListJob).__jobId)
                  return (
                  <div
                    className={`drive-table-row home${selectedKeys.includes(key) ? ' selected' : ''}`}
                    key={job.ID}
                    onContextMenu={canOpen ? (event) => openFileMenu(job, event) : undefined}
                    onClick={(event) => handleEntryClick(key, event)}
                    onDoubleClick={canOpen ? () => openFile(job.ID) : undefined}
                  >
                    <div className="drive-table-primary" role="button" tabIndex={0}>
                      <span className="drive-item-icon">{fileIconForJob(job)}</span>
                      <span className="drive-item-copy">
                        <span className="drive-item-title">{displayFilename(job.Filename)}</span>
                        {renderJobSub(job)}
                      </span>
                    </div>
                    {renderMobileHomeJobMeta(job)}
                    <span className="drive-table-meta mobile-type-source">{fileTypeLabel(job.FileType)}</span>
                    <span className="drive-table-meta mobile-date-source">{extractDate(job.UpdatedAt)}</span>
                    <button
                      className="drive-link-button"
                      onClick={() => navigate(job.FolderID ? `/files/folder/${job.FolderID}` : '/files/root')}
                      type="button"
                    >
                      {job.FolderName || '내 파일'}
                    </button>
                    {!canOpen ? <span className="drive-table-menu-col" /> : (
                      <button
                        aria-label={`${displayFilename(job.Filename)} 작업 열기`}
                        className="row-menu-button"
                        onClick={(event) => openFileMenu(job, event)}
                        type="button"
                      >
                        ⋮
                      </button>
                    )}
                  </div>
                  )
                })}
                {homeDisplayJobs.length === 0 ? <div className="empty-panel">표시할 파일이 없습니다.</div> : null}
              </div>
            </section>
            ) : null}
          </div>
        ) : null}

        {data && viewMode === 'search' ? (
          <section className="drive-section">
            <div className="drive-section-header">
              <h2>{query ? `"${query}" 검색 결과` : '검색 결과'}</h2>
            </div>
            {renderFilterToolbar()}
            <div className="drive-table">
              <div className="drive-table-header">
                <button className="column-sort-button" onClick={() => toggleSort('name')} type="button">
                  이름{renderSortMark(sortKey, sortDirection, 'name')}
                </button>
                <button className="column-sort-button" onClick={() => toggleSort('updated')} type="button">
                  수정 날짜{renderSortMark(sortKey, sortDirection, 'updated')}
                </button>
                <button className="column-sort-button" onClick={() => toggleSort('location')} type="button">
                  위치{renderSortMark(sortKey, sortDirection, 'location')}
                </button>
                <span className="drive-table-menu-col" />
              </div>
              {sortedHomeJobs.map((job) => {
                const key = entryKey('file', job.ID)
                return (
                  <div
                    className={`drive-table-row${selectedKeys.includes(key) ? ' selected' : ''}`}
                    key={job.ID}
                    onContextMenu={(event) => openFileMenu(job, event)}
                    onClick={(event) => handleEntryClick(key, event)}
                    onDoubleClick={() => openFile(job.ID)}
                  >
                    <div className="drive-table-primary" role="button" tabIndex={0}>
                      <span className="drive-item-icon">{fileIconForJob(job)}</span>
                      <span className="drive-item-copy">
                        <span className="drive-item-title">{displayFilename(job.Filename)}</span>
                        {renderJobSub(job)}
                      </span>
                    </div>
                    {renderMobileJobMeta(job)}
                    <span className="drive-table-meta mobile-date-source">{extractDate(job.UpdatedAt)}</span>
                    <button
                      className="drive-link-button"
                      onClick={() => navigate(job.FolderID ? `/files/folder/${job.FolderID}` : '/files/root')}
                      type="button"
                    >
                      {job.FolderName || '내 파일'}
                    </button>
                    <button
                      aria-label={`${displayFilename(job.Filename)} 작업 열기`}
                      className="row-menu-button"
                      onClick={(event) => openFileMenu(job, event)}
                      type="button"
                    >
                      ⋮
                    </button>
                  </div>
                )
              })}
              {sortedHomeJobs.length === 0 ? <div className="empty-panel">검색 결과가 없습니다.</div> : null}
            </div>
          </section>
        ) : null}

        {data && viewMode === 'explore' ? (
          <section
            className="drive-section explore-section"
            onClick={(event) => {
              if (event.target === event.currentTarget) {
                clearSelection()
              }
            }}
          >
            <div className="drive-pathbar">
              <div className="drive-path">
                <button className="drive-path-segment" onClick={() => navigate('/files/root')} type="button">
                  내 파일
                </button>
                {folderPath.map((folder) => (
                  <button
                    className={`drive-path-segment${folder.Name.length > 10 ? ' truncated' : ''}`}
                    key={folder.ID}
                    onClick={() => navigate(`/files/folder/${folder.ID}`)}
                    type="button"
                  >
                    {folder.Name}
                  </button>
                ))}
              </div>
              <div className="drive-path-meta">
                <button className="ghost-button small" onClick={() => openCreateFolderDialog(folderId, 'refresh')} type="button">
                  새 폴더
                </button>
                <button className="ghost-button small mobile-file-add-button" onClick={() => fileInputRef.current?.click()} type="button">
                  새 파일
                </button>
              </div>
            </div>

            {selectedEntries.length > 0 ? (
              <div className="selection-toolbar">
                <div className="selection-toolbar-inner">
                  <div className="selection-toolbar-main">
                    <button className="selection-toolbar-close" onClick={() => clearSelection()} type="button">
                      ×
                    </button>
                    <div className="selection-toolbar-copy">{selectedEntries.length}개 선택됨</div>
                  </div>
                  <div className="selection-toolbar-actions">
                    <button className="toolbar-button" onClick={() => void handleBulkDownload()} type="button">
                      다운로드
                    </button>
                    <button className="toolbar-button" onClick={() => void handleBulkMove()} type="button">
                      이동
                    </button>
                    <button className="toolbar-button danger" onClick={() => void handleBulkDelete()} type="button">
                      삭제
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
                {hasActiveFilters ? (
                  <div className="filter-group">
                    <button className="filter-reset-button" onClick={resetFilters} type="button">
                      필터 지우기
                    </button>
                  </div>
                ) : null}
              </div>
            )}

            <div
              className="drive-explorer-body"
              onDragOver={(event) => {
                if (hasExternalFiles(event.dataTransfer)) {
                  return
                }
                const target = event.target as HTMLElement
                if (!target.closest('.drive-table-row')) {
                  setDropTargetFolderId('')
                }
              }}
              onContextMenu={openSurfaceMenu}
              onMouseDown={beginSelectionBox}
              onClick={(event) => {
                if (event.target === event.currentTarget) {
                  clearSelection()
                }
              }}
              ref={driveTableRef}
            >
              <div className="drive-table">
                <div className="drive-table-header explore">
                  <button className="column-sort-button" onClick={() => toggleSort('name')} type="button">
                    이름{renderSortMark(sortKey, sortDirection, 'name')}
                  </button>
                  <button className="column-sort-button" onClick={() => toggleSort('kind')} type="button">
                    종류{renderSortMark(sortKey, sortDirection, 'kind')}
                  </button>
                  <button className="column-sort-button" onClick={() => toggleSort('updated')} type="button">
                    수정 날짜{renderSortMark(sortKey, sortDirection, 'updated')}
                  </button>
                  <span>파일 길이</span>
                  <span>파일 크기</span>
                  <span className="drive-table-menu-col" />
                </div>
                {sortedExploreEntries.map((entry) =>
                  entry.kind === 'folder' ? (
                    <div
                      className={`drive-table-row explore${selectedKeys.includes(entry.key) ? ' selected' : ''}${dropTargetFolderId === entry.item.ID ? ' drop-target' : ''}`}
                      draggable
                      key={entry.key}
                      onClick={(event) => handleEntryClick(entry.key, event)}
                      onContextMenu={(event) => openFolderMenu(entry.item, event)}
                      onDoubleClick={() => openFolder(entry.item.ID)}
                      onDragEnd={handleRowDragEnd}
                      onDragLeave={(event) => handleFolderDragLeave(entry.item.ID, event)}
                      onDragOver={(event) => handleFolderDragOver(entry.item.ID, event)}
                      onDragStart={(event) => handleRowDragStart(entry.key, event)}
                      onDrop={(event) => void handleFolderDrop(entry.item.ID, event)}
                      ref={(node) => {
                        rowRefs.current[entry.key] = node
                      }}
                    >
                      <div className="drive-table-primary" role="button" tabIndex={0}>
                        <span className="drive-item-icon">📁</span>
                        <span className="drive-item-copy">
                          <span className="drive-item-title">{entry.item.Name}</span>
                        </span>
                      </div>
                      <span className="drive-table-meta mobile-type-source">폴더</span>
                      <span className="drive-table-meta mobile-date-source">{extractDate(entry.item.UpdatedAt)}</span>
                      <span className="drive-table-meta mobile-duration-source">-</span>
                      <span className="drive-table-meta mobile-size-source">-</span>
                      <button
                        aria-label={`${entry.item.Name} 작업 열기`}
                        className="row-menu-button"
                        onClick={(event) => openFolderMenu(entry.item, event)}
                        type="button"
                      >
                        ⋮
                      </button>
                    </div>
                  ) : (
                    (() => {
                      const isPending = Boolean((entry.item as FileListJob).__pending)
                      const canOpen = !isPending || Boolean((entry.item as FileListJob).__jobId)
                      return (
                        <div
                          className={`drive-table-row explore${selectedKeys.includes(entry.key) ? ' selected' : ''}`}
                          draggable={!isPending}
                          key={entry.key}
                          onClick={(event) => handleEntryClick(entry.key, event)}
                          onContextMenu={canOpen ? (event) => openFileMenu(entry.item, event) : undefined}
                          onDoubleClick={canOpen ? () => openFile(entry.item.ID) : undefined}
                          onDragEnd={isPending ? undefined : handleRowDragEnd}
                          onDragStart={isPending ? undefined : (event) => handleRowDragStart(entry.key, event)}
                          ref={(node) => {
                            rowRefs.current[entry.key] = node
                          }}
                        >
                          <div className="drive-table-primary" role="button" tabIndex={0}>
                            <span className="drive-item-icon">{fileIconForJob(entry.item)}</span>
                            <span className="drive-item-copy">
                              <span className="drive-item-title">{displayFilename(entry.item.Filename)}</span>
                              {renderJobSub(entry.item)}
                            </span>
                          </div>
                          {renderMobileJobMeta(entry.item)}
                          <span className="drive-table-meta mobile-type-source">{fileTypeLabel(entry.item.FileType)}</span>
                          <span className="drive-table-meta mobile-date-source">{extractDate(entry.item.UpdatedAt)}</span>
                          <span className="drive-table-meta mobile-duration-source">{entry.item.MediaDuration || '-'}</span>
                          <span className="drive-table-meta mobile-size-source">{formatBytes(entry.item.SizeBytes)}</span>
                          {!canOpen ? (
                            <span className="drive-table-menu-col" />
                          ) : (
                            <button
                              aria-label={`${displayFilename(entry.item.Filename)} 작업 열기`}
                              className="row-menu-button"
                              onClick={(event) => openFileMenu(entry.item, event)}
                              type="button"
                            >
                              ⋮
                            </button>
                          )}
                        </div>
                      )
                    })()
                  ),
                )}
              </div>
              {selectionBox ? <div className="selection-box" style={selectionBoxStyle(selectionBox)} /> : null}
            </div>
          </section>
        ) : null}

        {data && viewMode !== 'home' && data.total_pages > 1 ? (
          <div className="pagination">
            {Array.from({ length: data.total_pages }, (_, index) => index + 1).map((pageNumber) => (
              <button
                className={`ghost-button${pageNumber === page ? ' active' : ''}`}
                key={pageNumber}
                onClick={() => updateQuery(searchParams, setSearchParams, { page: String(pageNumber) })}
                type="button"
              >
                {pageNumber}
              </button>
            ))}
          </div>
        ) : null}
      </section>

      {menuState ? (
        <div className="context-menu" style={{ left: clampMenu(menuState.x), top: clampMenu(menuState.y) }}>
          {menuState.kind === 'surface' ? (
            <>
              <button
                className="context-menu-item"
                onClick={() => {
                  setMenuState(null)
                  fileInputRef.current?.click()
                }}
                type="button"
              >
                새 파일
              </button>
              <button
                className="context-menu-item"
                onClick={() => {
                  setMenuState(null)
                  openCreateFolderDialog(folderId, 'refresh')
                }}
                type="button"
              >
                새 폴더
              </button>
            </>
          ) : menuState.kind === 'folder' ? (
            <>
              <button
                className="context-menu-item"
                disabled={selectedKeys.length !== 1}
                onClick={() => void handleRenameFromMenu()}
                type="button"
              >
                이름 변경
              </button>
              <button className="context-menu-item" onClick={() => void handleFolderAction('download', menuState.item)} type="button">
                다운로드
              </button>
              <button className="context-menu-item" onClick={() => void handleFolderAction('move', menuState.item)} type="button">
                이동
              </button>
              <button className="context-menu-item danger" onClick={() => void handleFolderAction('delete', menuState.item)} type="button">
                삭제
              </button>
            </>
          ) : (
            <>
              <button
                className="context-menu-item"
                disabled={selectedKeys.length !== 1}
                onClick={() => void handleRenameFromMenu()}
                type="button"
              >
                이름 변경
              </button>
              <button className="context-menu-item" onClick={() => void handleFileAction('download', menuState.item)} type="button">
                다운로드
              </button>
              <button className="context-menu-item" onClick={() => void handleFileAction('move', menuState.item)} type="button">
                이동
              </button>
              <button className="context-menu-item danger" onClick={() => void handleFileAction('delete', menuState.item)} type="button">
                삭제
              </button>
            </>
          )}
        </div>
      ) : null}

      {moveState ? (
        <div className="modal-shell">
          <div className="modal-card move-modal-card">
            <h3>{moveTitle}</h3>
            <div className="move-current-path">
              {moveCurrentPath.map((segment) => (
                <button
                  className="move-path-segment"
                  key={segment.id || 'root'}
                  onClick={() => {
                    setMoveBrowserFolderId(segment.id)
                    setMoveTargetId(segment.id)
                  }}
                  type="button"
                >
                  {segment.label}
                </button>
              ))}
            </div>
            <div className="move-folder-list">
              {moveFolderChildren.map((folder) => (
                <button
                  className={`move-folder-row${moveTargetId === folder.ID ? ' active' : ''}`}
                  key={folder.ID}
                  onClick={() => {
                    setMoveBrowserFolderId(folder.ID)
                    setMoveTargetId(folder.ID)
                  }}
                  type="button"
                >
                  <span className="drive-item-icon">📁</span>
                  <span>{folder.Name}</span>
                </button>
              ))}
              {moveFileChildren.map((job) => (
                <div className="move-folder-row is-disabled" key={job.ID}>
                  <span className="drive-item-icon">{fileIconForJob(job)}</span>
                  <span>{job.Filename}</span>
                </div>
              ))}
              {moveFolderChildren.length === 0 && moveFileChildren.length === 0 ? (
                <div className="empty-panel move-empty">이 위치에 항목이 없습니다.</div>
              ) : null}
            </div>
            <div className="move-modal-footer">
              <button
                className="toolbar-button"
                onClick={() => openCreateFolderDialog(moveBrowserFolderId, 'move-browser')}
                type="button"
              >
                새 폴더
              </button>
              <div className="move-modal-actions">
                <button
                  className="ghost-button"
                  onClick={() => {
                    setMoveState(null)
                    setMoveBrowserFolderId('')
                    setMoveTargetId('')
                  }}
                  type="button"
                >
                  취소
                </button>
                <button className="primary-button" onClick={() => void handleMove()} type="button">
                  이동
                </button>
              </div>
            </div>
          </div>
        </div>
      ) : null}

      {uploadBatchState ? (
        <div className="modal-shell">
          <div className="modal-card upload-modal-card">
            <div className="upload-modal-header">
              <div>
                <h3>업로드</h3>
                <div className="upload-modal-summary">
                  <span>{uploadBatchState.items.length}개 파일</span>
                  <span>{uploadBatchState.folderId ? currentFolderName(uploadBatchState.folderId, allFolders) : '내 파일'}에 업로드</span>
                </div>
              </div>
            </div>
            <form className="stack-form" onSubmit={handleUploadSubmit}>
              <div className="upload-description-head">
                <label>설명</label>
                <div className="upload-description-mode" role="group" aria-label="설명 입력 방식">
                  <button
                    className={`ghost-button small${uploadBatchState.descriptionMode === 'shared' ? ' active' : ''}`}
                    onClick={() => setUploadBatchState((current) => (current ? { ...current, descriptionMode: 'shared' } : current))}
                    type="button"
                  >
                    공통 설명
                  </button>
                  <button
                    className={`ghost-button small${uploadBatchState.descriptionMode === 'per_file' ? ' active' : ''}`}
                    onClick={() => setUploadBatchState((current) => (current ? { ...current, descriptionMode: 'per_file' } : current))}
                    type="button"
                  >
                    파일별 설명
                  </button>
                </div>
              </div>
              <div className={`upload-file-list ${uploadBatchState.descriptionMode === 'per_file' ? 'per-file' : 'shared'}`}>
                <div className="upload-file-list-header" aria-hidden="true">
                  <span>파일</span>
                  <span>{uploadBatchState.descriptionMode === 'per_file' ? '설명' : '정제 여부'}</span>
                </div>
                {uploadBatchState.items.map((item, index) => (
                  <section className="upload-file-row" key={`${item.file.name}-${item.file.size}-${index}`}>
                    <div className="upload-file-main">
                      <span className="upload-file-type-badge">{fileTypeLabel(item.fileType)}</span>
                      <div className="upload-file-copy">
                        <input
                          aria-label={`${item.file.name} 파일명`}
                          className="dark-input upload-file-name-input"
                          onChange={(event) => updateUploadItem(index, { displayName: event.target.value })}
                          placeholder={stripExtension(item.file.name)}
                          value={item.displayName}
                        />
                        {uploadBatchState.descriptionMode === 'per_file' ? (
                          <div className="upload-refine-cell">
                            {item.fileType === 'pdf' ? (
                              <div className="upload-file-note">PDF 정제 없음</div>
                            ) : (
                              <label className="upload-refine-toggle">
                                <input checked={item.refineEnabled} onChange={(event) => updateUploadItem(index, { refineEnabled: event.target.checked })} type="checkbox" />
                                <span>전사 후 정제</span>
                              </label>
                            )}
                          </div>
                        ) : null}
                      </div>
                    </div>
                    {uploadBatchState.descriptionMode === 'shared' ? (
                      <div className="upload-refine-cell">
                        {item.fileType === 'pdf' ? (
                          <div className="upload-file-note">
                            PDF 정제 없음 · 최대 {pdfMaxPages || '-'}페이지, 요청당 {pdfMaxPagesPerRequest || '-'}페이지
                          </div>
                        ) : (
                          <label className="upload-refine-toggle">
                            <input checked={item.refineEnabled} onChange={(event) => updateUploadItem(index, { refineEnabled: event.target.checked })} type="checkbox" />
                            <span>전사 후 정제</span>
                          </label>
                        )}
                      </div>
                    ) : null}
                    {uploadBatchState.descriptionMode === 'per_file' ? (
                      <div className="upload-file-description">
                        <textarea
                          className="dark-input"
                          onChange={(event) => updateUploadItem(index, { description: event.target.value })}
                          placeholder="설명"
                          rows={2}
                          value={item.description}
                        />
                      </div>
                    ) : null}
                  </section>
                ))}
              </div>
              {uploadBatchState.descriptionMode === 'shared' ? (
                <div className="upload-description-panel">
                  <label>공통 설명</label>
                  <textarea
                    className="dark-input"
                    onChange={(event) => setUploadBatchState((current) => (current ? { ...current, sharedDescription: event.target.value } : current))}
                    rows={2}
                    value={uploadBatchState.sharedDescription}
                  />
                </div>
              ) : null}
              <div className="modal-actions">
                <button className="ghost-button" onClick={() => setUploadBatchState(null)} type="button">
                  취소
                </button>
                <button className="primary-button" type="submit">
                  {isSingleUpload ? '업로드 시작' : '순차 업로드 시작'}
                </button>
              </div>
            </form>
          </div>
        </div>
      ) : null}

      {textDialog ? (
        <div className="modal-shell">
          <div className="modal-card">
            <h3>{textDialog.title}</h3>
            <form className="stack-form" onSubmit={submitTextDialog}>
              <div className="label-field">
                <label>{textDialog.label}</label>
                <input
                  autoFocus
                  className="dark-input"
                  onChange={(event) => setTextDialog((current) => (current ? { ...current, value: event.target.value } : current))}
                  value={textDialog.value}
                />
              </div>
              <div className="modal-actions">
                <button className="ghost-button" onClick={() => setTextDialog(null)} type="button">
                  취소
                </button>
                <button className="primary-button" type="submit">
                  {textDialog.submitLabel}
                </button>
              </div>
            </form>
          </div>
        </div>
      ) : null}

      {confirmDialog ? (
        <div className="modal-shell">
          <div className="modal-card">
            <h3>{confirmDialog.title}</h3>
            <p className="modal-text">{confirmDialog.body}</p>
            <div className="modal-actions">
              <button className="ghost-button" onClick={() => setConfirmDialog(null)} type="button">
                취소
              </button>
              <button className="primary-button danger" onClick={() => void handleConfirmDelete()} type="button">
                삭제
              </button>
            </div>
          </div>
        </div>
      ) : null}

      {error || message ? (
        <div className="toast-stack">
          {error ? <div className="alert error toast-alert">{error}</div> : null}
          {message ? <div className="alert info toast-alert">{message}</div> : null}
        </div>
      ) : null}
    </div>
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
