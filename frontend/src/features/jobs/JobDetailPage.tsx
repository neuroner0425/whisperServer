import { useEffect, useMemo, useState } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'

import { fetchJobDetail } from './api'
import type { JobDetailResponse } from './types'

export function JobDetailPage() {
  const { jobId = '' } = useParams()
  const [searchParams, setSearchParams] = useSearchParams()
  const [data, setData] = useState<JobDetailResponse | null>(null)
  const [error, setError] = useState('')
  const [isLoading, setIsLoading] = useState(true)
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

  return (
    <section className="view-shell job-detail-view">
      <header className="view-header">
        <div>
          <p className="view-eyebrow">FILE</p>
          <h1 className="view-title">{currentFileName}</h1>
        </div>
        <div className="view-actions">
          {downloadHref ? (
            <a className="primary-button" href={downloadHref}>
              다운로드
            </a>
          ) : null}
        </div>
      </header>

      {error ? <div className="alert error">{error}</div> : null}
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

            {data.view !== 'result' ? (
              <div className="progress-shell">
                <div className="progress-label">{progressText || '처리 중'}</div>
                <div className="progress-track dark">
                  <div className="progress-bar" style={{ width: `${data.job.ProgressPercent}%` }} />
                </div>
              </div>
            ) : null}

            <pre className="result-panel dark">
              {data.view === 'result' ? data.text || '' : data.view === 'preview' ? data.original_text || '' : data.preview_text || ''}
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
