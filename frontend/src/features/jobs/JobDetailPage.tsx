import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'

import { fetchFiles, moveEntries, trashJob } from '../files/api'
import type { FolderNode } from '../files/types'
import { fetchJobDetail } from './api'
import type { JobDetailResponse } from './types'

export function JobDetailPage() {
  const navigate = useNavigate()
  const { jobId = '' } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const [data, setData] = useState<JobDetailResponse | null>(null)
  const [error, setError] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [folders, setFolders] = useState<FolderNode[]>([])
  const [moveTargetId, setMoveTargetId] = useState('')
  const [showMoveModal, setShowMoveModal] = useState(false)
  const [showDeleteModal, setShowDeleteModal] = useState(false)
  const [message, setMessage] = useState('')

  const showOriginal = searchParams.get('original') === 'true'
  const currentFileName = displayFilename(data?.job.Filename || '파일')
  const progressText = `${data?.job.Phase || ''} ${data?.job.ProgressPercent ?? 0}%`.trim()

  useEffect(() => {
    let closed = false
    const controller = new AbortController()

    async function load(force: boolean) {
      if (force) {
        setIsLoading(true)
      }
      try {
        const payload = await fetchJobDetail(jobId, showOriginal, controller.signal)
        if (!closed) {
          setData(payload)
          setError('')
        }
      } catch (loadError) {
        if (!closed && !controller.signal.aborted) {
          setError(loadError instanceof Error ? loadError.message : '작업을 불러오지 못했습니다.')
        }
      } finally {
        if (!closed && force) {
          setIsLoading(false)
        }
      }
    }

    void load(true)
    const shouldPoll = data?.view !== 'result'
    const timer = shouldPoll
      ? window.setInterval(() => {
          void load(false)
        }, 3000)
      : undefined

    return () => {
      closed = true
      controller.abort()
      if (timer) {
        window.clearInterval(timer)
      }
    }
  }, [jobId, showOriginal, data?.view])

  useEffect(() => {
    let closed = false
    const controller = new AbortController()

    async function loadFolders() {
      try {
        const payload = await fetchFiles({
          viewMode: 'explore',
          folderId: '',
          query: '',
          tag: '',
          sort: 'updated',
          order: 'desc',
          page: 1,
          signal: controller.signal,
        })
        if (!closed && payload) {
          setFolders(payload.all_folders ?? [])
        }
      } catch (loadError) {
        if (!closed && !controller.signal.aborted) {
          setError(loadError instanceof Error ? loadError.message : '폴더 목록을 불러오지 못했습니다.')
        }
      }
    }

    void loadFolders()
    return () => {
      closed = true
      controller.abort()
    }
  }, [])

  const toggleOriginal = (enabled: boolean) => {
    const next = new URLSearchParams(searchParams)
    if (enabled) {
      next.set('original', 'true')
    } else {
      next.delete('original')
    }
    setSearchParams(next)
  }

  const handleDelete = async () => {
    try {
      await trashJob(jobId)
      setShowDeleteModal(false)
      navigate('/files/trash')
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '파일을 삭제하지 못했습니다.')
    }
  }

  const handleMove = async () => {
    try {
      await moveEntries([jobId], [], moveTargetId)
      setShowMoveModal(false)
      setMoveTargetId('')
      setMessage('파일을 이동했습니다.')
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '파일 이동에 실패했습니다.')
    }
  }

  const downloadHref = useMemo(() => {
    if (!data) {
      return ''
    }
    return data.has_refined && data.variant !== 'original' ? data.download_refined_url || data.download_text_url || '' : data.download_text_url || ''
  }, [data])

  return (
    <section className="view-shell job-detail-view">
      <header className="view-header">
        <div>
          <p className="view-eyebrow">FILE</p>
          <h1 className="view-title">{currentFileName}</h1>
          <p className="view-description">전사 결과, 처리 상태, 파일 작업을 한 화면에서 확인합니다.</p>
        </div>
        <div className="view-actions">
          <button className="ghost-button" onClick={() => setShowMoveModal(true)} type="button">
            이동
          </button>
          <button className="ghost-button danger" onClick={() => setShowDeleteModal(true)} type="button">
            삭제
          </button>
          {downloadHref ? (
            <a className="primary-button" href={downloadHref}>
              다운로드
            </a>
          ) : null}
        </div>
      </header>

      {error ? <div className="alert error">{error}</div> : null}
      {message ? <div className="alert info">{message}</div> : null}
      {isLoading && !data ? <div className="empty-panel">파일 정보를 불러오는 중입니다.</div> : null}

      {data ? (
        <>
          <section className="detail-list plain">
            <div className="detail-row compact">
              <span className="detail-label">상태</span>
              <span className="detail-value">{data.job.Status}</span>
            </div>
            <div className="detail-row compact">
              <span className="detail-label">업로드</span>
              <span className="detail-value">{data.job.UploadedAt || '-'}</span>
            </div>
            <div className="detail-row compact">
              <span className="detail-label">길이</span>
              <span className="detail-value">{data.job.MediaDuration || '-'}</span>
            </div>
            <div className="detail-row compact">
              <span className="detail-label">계정</span>
              <span className="detail-value">{data.current_user_name || '-'}</span>
            </div>
          </section>

          <section className="detail-content-section">
            <div className="detail-content-header">
              <h2>파일 내용</h2>
              <div className="toolbar-actions">
                {data.has_refined ? (
                  <>
                    <button className={`ghost-button small${showOriginal ? '' : ' active'}`} onClick={() => toggleOriginal(false)} type="button">
                      정제본
                    </button>
                    <button className={`ghost-button small${showOriginal ? ' active' : ''}`} onClick={() => toggleOriginal(true)} type="button">
                      원본
                    </button>
                  </>
                ) : null}
              </div>
            </div>

            {data.view !== 'result' ? (
              <div className="progress-shell">
                <div className="progress-label">{progressText || '처리 중'}</div>
                <div className="progress-track dark">
                  <div className="progress-bar" style={{ width: `${data.job.ProgressPercent}%` }} />
                </div>
              </div>
            ) : null}

            <pre className="result-panel dark plain">
              {data.view === 'result' ? data.text || '' : data.view === 'preview' ? data.original_text || '' : data.preview_text || ''}
            </pre>
          </section>
        </>
      ) : null}

      {showMoveModal ? (
        <div className="modal-shell">
          <div className="modal-card">
            <h3>파일 이동</h3>
            <p className="modal-text">{currentFileName}</p>
            <select className="dark-select full-width" onChange={(event) => setMoveTargetId(event.target.value)} value={moveTargetId}>
              <option value="">내 파일 (루트)</option>
              {folders.map((folder) => (
                <option key={folder.ID} value={folder.ID}>
                  {folder.Name}
                </option>
              ))}
            </select>
            <div className="modal-actions">
              <button className="ghost-button" onClick={() => setShowMoveModal(false)} type="button">
                취소
              </button>
              <button className="primary-button" onClick={() => void handleMove()} type="button">
                이동
              </button>
            </div>
          </div>
        </div>
      ) : null}

      {showDeleteModal ? (
        <div className="modal-shell">
          <div className="modal-card">
            <h3>파일 삭제</h3>
            <p className="modal-text">"{currentFileName}" 파일을 휴지통으로 보낼까요?</p>
            <div className="modal-actions">
              <button className="ghost-button" onClick={() => setShowDeleteModal(false)} type="button">
                취소
              </button>
              <button className="primary-button danger" onClick={() => void handleDelete()} type="button">
                삭제
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  )
}

function displayFilename(filename: string) {
  const dotIndex = filename.lastIndexOf('.')
  const ext = dotIndex > 0 ? filename.slice(dotIndex + 1).toLowerCase() : ''
  if (ext === 'mp3' || ext === 'wav' || ext === 'm4a') {
    return dotIndex > 0 ? filename.slice(0, dotIndex) : filename
  }
  return filename
}
