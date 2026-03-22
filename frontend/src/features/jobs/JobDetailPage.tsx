import { useEffect, useMemo, useState } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'

import { fetchJobDetail, refineJob, retryJob } from './api'
import type { JobDetailResponse } from './types'

export function JobDetailPage() {
  const { jobId = '' } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const [data, setData] = useState<JobDetailResponse | null>(null)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [isRetrying, setIsRetrying] = useState(false)
  const [isRefining, setIsRefining] = useState(false)
  const [showMeta, setShowMeta] = useState(false)

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
          setError(normalizeLoadError(loadError, '작업을 불러오지 못했습니다.'))
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

  const toggleOriginal = (enabled: boolean) => {
    const next = new URLSearchParams(searchParams)
    if (enabled) {
      next.set('original', 'true')
    } else {
      next.delete('original')
    }
    setSearchParams(next)
  }

  const downloadHref = useMemo(() => {
    if (!data) {
      return ''
    }
    return data.has_refined && data.variant !== 'original' ? data.download_refined_url || data.download_text_url || '' : data.download_text_url || ''
  }, [data])
  const displayText = useMemo(() => {
    if (!data) {
      return ''
    }
    const rawText =
      data.view === 'result'
        ? data.text || ''
        : data.view === 'preview'
          ? data.original_text || ''
          : data.preview_text || ''
    return normalizeTranscriptText(rawText)
  }, [data])

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

  const handleRetry = async () => {
    if (!jobId || isRetrying) {
      return
    }
    try {
      setIsRetrying(true)
      await retryJob(jobId)
      setMessage('전사를 다시 시작했습니다.')
      const payload = await fetchJobDetail(jobId, showOriginal)
      setData(payload)
    } catch (retryError) {
      setError(normalizeLoadError(retryError, '재시도에 실패했습니다.'))
    } finally {
      setIsRetrying(false)
    }
  }

  const handleRefine = async () => {
    if (!jobId || isRefining) {
      return
    }
    try {
      setIsRefining(true)
      await refineJob(jobId)
      setMessage('정제를 시작했습니다.')
      const payload = await fetchJobDetail(jobId, showOriginal)
      setData(payload)
    } catch (refineError) {
      setError(normalizeLoadError(refineError, '정제를 시작하지 못했습니다.'))
    } finally {
      setIsRefining(false)
    }
  }

  return (
    <section className="view-shell job-detail-view">
      <header className="view-header">
        <div>
          <p className="view-eyebrow">FILE</p>
          <h1 className="view-title">{currentFileName}</h1>
        </div>
        <div className="view-actions">
          {data?.status === '완료' && !data?.has_refined && data?.can_refine ? (
            <button className="ghost-button" disabled={isRefining} onClick={() => void handleRefine()} type="button">
              {isRefining ? '정제 시작 중...' : '정제하기'}
            </button>
          ) : null}
          {data?.status === '실패' ? (
            <button className="ghost-button" disabled={isRetrying} onClick={() => void handleRetry()} type="button">
              {isRetrying ? '재시도 중...' : '재시도'}
            </button>
          ) : null}
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
          <section className="detail-meta-section">
            <button className="detail-meta-toggle" onClick={() => setShowMeta((current) => !current)} type="button">
              {showMeta ? '상세 정보 숨기기' : '상세 정보 보기'}
            </button>
            {showMeta ? (
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
            ) : null}
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

            {data.audio_url ? (
              <div className="audio-player-shell">
                <audio controls preload="metadata" src={data.audio_url} />
              </div>
            ) : null}

            {data.view !== 'result' ? (
              <div className="progress-shell">
                <div className="progress-label">{progressText || '처리 중'}</div>
                <div className="progress-track dark">
                  <div className="progress-bar" style={{ width: `${data.job.ProgressPercent}%` }} />
                </div>
              </div>
            ) : null}

            <pre className="result-panel dark">
              {displayText}
            </pre>
          </section>
        </>
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

function normalizeTranscriptText(text: string) {
  if (!text.trim()) {
    return ''
  }
  return text
    .replace(/\r\n/g, '\n')
    .split('\n')
    .map((line) =>
      line
        .replace(/^\s*\[[^\]]*-->\s*[^\]]*\]\s*/g, '')
        .trim(),
    )
    .filter((line) => line.length > 0)
    .join('\n')
}

function isPersistentNetworkError(error: string) {
  return error === 'Failed to fetch' || error.includes('서버와 연결할 수 없습니다.')
}

function normalizeLoadError(error: unknown, fallback: string) {
  if (error instanceof Error && error.message === 'Failed to fetch') {
    return '서버와 연결할 수 없습니다.'
  }
  return error instanceof Error ? error.message : fallback
}
